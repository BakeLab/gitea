// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestSkillMatchesCommands checks the agent skill in skills/gt: frontmatter as the Agent Skills
// specification requires, links that resolve, and only gt commands and flags that exist.
func TestSkillMatchesCommands(t *testing.T) {
	dir := filepath.Join("..", "..", "skills", "gt")
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	require.NoError(t, err)
	text := strings.ReplaceAll(string(data), "\r\n", "\n") // a Windows checkout may convert line endings
	front, body, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	require.True(t, ok && strings.HasPrefix(text, "---\n"), "SKILL.md needs YAML frontmatter")
	var meta struct{ Name, Description string }
	require.NoError(t, yaml.Unmarshal([]byte(front), &meta))
	assert.Regexp(t, `^[a-z0-9]+(-[a-z0-9]+)*$`, meta.Name)
	assert.Equal(t, filepath.Base(dir), meta.Name)
	assert.NotEmpty(t, meta.Description)
	assert.LessOrEqual(t, len(meta.Description), 1024)

	files := map[string]string{"SKILL.md": body}
	references, err := filepath.Glob(filepath.Join(dir, "references", "*.md"))
	require.NoError(t, err)
	for _, path := range references {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		name := "references/" + filepath.Base(path)
		files[name] = strings.ReplaceAll(string(data), "\r\n", "\n")
		assert.Contains(t, body, "]("+name+")", "SKILL.md needs to link %s", name)
	}

	deps, _, _ := testDeps(t)
	root := NewRoot(deps)
	checked := 0
	for name, text := range files {
		for _, match := range markdownLink.FindAllStringSubmatch(text, -1) {
			path, anchor := match[1], match[2]
			if strings.Contains(path, "://") {
				continue
			}
			target := name
			if path != "" {
				target = filepath.ToSlash(filepath.Join(filepath.Dir(name), path))
				_, err := os.Stat(filepath.Join(dir, target))
				assert.NoError(t, err, "%s links to %s", name, path)
			}
			if anchor != "" {
				assert.Contains(t, headingAnchors(files[target]), anchor, "%s links to %s#%s", name, path, anchor)
			}
		}
		for _, words := range gtInvocations(text) {
			checked++
			if err := checkInvocation(root, words); err != nil {
				t.Errorf("%s: gt %s: %v", name, strings.Join(words, " "), err)
			}
		}
	}
	assert.Positive(t, checked, "the skill's gt commands were not found")
}

var (
	markdownLink = regexp.MustCompile(`\]\(([^)#\s]*)(?:#([^)\s]*))?\)`)
	inlineCode   = regexp.MustCompile("`([^`]+)`")
	heredoc      = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?`)
	yamlRun      = regexp.MustCompile(`^(\s*)(?:- )?run:\s*(.*)$`)
	notInAnchor  = regexp.MustCompile(`[^\p{L}\p{N} _-]`)
	placeholder  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	commandWord  = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`) // a name, as opposed to a number, URL or path
)

// headingAnchors returns the anchors that Gitea and GitHub give the headings of a Markdown text.
func headingAnchors(text string) []string {
	var anchors []string
	fenced := false
	for line := range strings.Lines(text) {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
		}
		if heading := strings.TrimLeft(line, "#"); !fenced && len(heading) < len(line) && strings.HasPrefix(heading, " ") {
			anchor := notInAnchor.ReplaceAllString(strings.ToLower(strings.TrimSpace(heading)), "")
			anchors = append(anchors, strings.ReplaceAll(anchor, " ", "-"))
		}
	}
	return anchors
}

// gtInvocations returns the arguments of each gt command in the inline code, the shell code
// blocks and the run steps of YAML workflows in a Markdown text, skipping heredoc bodies.
func gtInvocations(text string) [][]string {
	var invocations [][]string
	fenced, shell, workflow := false, false, false
	var delimiter, runIndent string
	for line := range strings.Lines(text) {
		if delimiter != "" {
			if strings.TrimSpace(line) == delimiter {
				delimiter = ""
			}
			continue
		}
		if lang, ok := strings.CutPrefix(strings.TrimSpace(line), "```"); ok {
			fenced, shell = !fenced, slices.Contains([]string{"", "sh", "bash", "shell"}, lang)
			workflow, runIndent = fenced && lang == "yaml", ""
			continue
		}
		if match := heredoc.FindStringSubmatch(line); fenced && shell && match != nil {
			delimiter = match[1]
		}
		var snippets []string
		switch {
		case !fenced:
			for _, match := range inlineCode.FindAllStringSubmatch(line, -1) {
				snippets = append(snippets, match[1])
			}
		case shell:
			snippets = []string{line}
		case workflow:
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			if runIndent != "" && len(indent) > len(runIndent) {
				snippets = []string{line}
			} else if match := yamlRun.FindStringSubmatch(strings.TrimRight(line, "\n")); match != nil {
				runIndent = ""
				if script := strings.TrimSpace(match[2]); script == "|" || script == ">" {
					runIndent = match[1] + " "
				} else {
					snippets = []string{script}
				}
			} else {
				runIndent = ""
			}
		}
		for _, snippet := range snippets {
			for _, words := range shellCommands(snippet) {
				if words[0] == "gt" && len(words) > 1 {
					invocations = append(invocations, words[1:])
				}
			}
		}
	}
	return invocations
}

// shellCommands splits a line of shell into the words of its simple commands, which pipes,
// lists, redirections, subshells and comments end.
func shellCommands(line string) [][]string {
	var commands [][]string
	var words []string
	var word strings.Builder
	var quote rune
	inWord := false
	endWord := func() {
		if inWord {
			words, inWord = append(words, word.String()), false
			word.Reset()
		}
	}
	endCommand := func() {
		endWord()
		if len(words) > 0 {
			commands, words = append(commands, words), nil
		}
	}
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(r)
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			endWord()
		case r == '#' && !inWord:
			endCommand()
			return commands
		case strings.ContainsRune("|&;<>()", r):
			endCommand()
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	endCommand()
	return commands
}

// checkInvocation resolves the command that words name below root and checks its flags, the
// values of enum flags, the fields that --json selects, and the topics that gt help names.
func checkInvocation(root *cobra.Command, words []string) error {
	cmd, rest := root, words
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		child := subcommand(cmd, rest[0])
		if child == nil {
			break
		}
		cmd, rest = child, rest[1:]
	}
	if cmd.Name() == "help" && cmd.Parent() == root && len(rest) > 0 {
		if _, extra, err := root.Find(rest); err != nil || len(extra) > 0 {
			return fmt.Errorf("gt help has no topic %q", strings.Join(rest, " "))
		}
		return nil
	}
	cmd.InitDefaultHelpFlag() // cobra adds --help and --version only when it executes
	cmd.InitDefaultVersionFlag()
	lookup := func(name string, short bool) *pflag.Flag {
		if short {
			return cmp.Or(cmd.Flags().ShorthandLookup(name), cmd.InheritedFlags().ShorthandLookup(name))
		}
		return cmp.Or(cmd.Flags().Lookup(name), cmd.InheritedFlags().Lookup(name))
	}
	var pending *pflag.Flag // a flag whose value is the next word
	for _, word := range rest {
		if pending != nil {
			if err := checkFlagValue(cmd, pending, word); err != nil {
				return err
			}
			pending = nil
			continue
		}
		switch {
		case word == "-" || !strings.HasPrefix(word, "-"):
			if cmd == root || cmd.Annotations["gt.group"] != "" || (cmd.HasSubCommands() && commandWord.MatchString(word)) {
				return fmt.Errorf("%s has no command %q", cmd.CommandPath(), word)
			}
		case strings.HasPrefix(word, "--"):
			name, value, hasValue := strings.Cut(word[2:], "=")
			flag := lookup(name, false)
			if flag == nil {
				return fmt.Errorf("%s has no flag --%s", cmd.CommandPath(), name)
			}
			if hasValue {
				if err := checkFlagValue(cmd, flag, value); err != nil {
					return err
				}
			} else if flag.NoOptDefVal == "" {
				pending = flag
			}
		case len(word) == 2:
			flag := lookup(word[1:], true)
			if flag == nil {
				return fmt.Errorf("%s has no flag %s", cmd.CommandPath(), word)
			}
			if flag.NoOptDefVal == "" {
				pending = flag
			}
		default:
			return fmt.Errorf("write %s as separate short flags or a long flag", word)
		}
	}
	return nil
}

// checkFlagValue checks the fields that --json selects and the values of enum flags; values
// that the shell expands and placeholders such as OWNER are not known.
func checkFlagValue(cmd *cobra.Command, flag *pflag.Flag, value string) error {
	if strings.Contains(value, "$") || (placeholder.MatchString(value) && flag.Annotations[enumAnnotation] == nil) {
		return nil
	}
	if flag.Name == "json" {
		return checkFields(cmd, value)
	}
	allowed := flag.Annotations[enumAnnotation]
	if allowed == nil {
		return nil
	}
	values := []string{value}
	if _, ok := flag.Value.(pflag.SliceValue); ok {
		values = strings.Split(value, ",")
	}
	for _, value := range values {
		if !slices.ContainsFunc(allowed, func(v string) bool { return strings.EqualFold(v, value) }) {
			return fmt.Errorf("%s --%s has no value %q", cmd.CommandPath(), flag.Name, value)
		}
	}
	return nil
}

func checkFields(cmd *cobra.Command, fields string) error {
	for field := range strings.SplitSeq(fields, ",") {
		if !slices.Contains(commandOutputFields(cmd), field) {
			return fmt.Errorf("%s has no JSON field %q", cmd.CommandPath(), field)
		}
	}
	return nil
}

func subcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == name || child.HasAlias(name) {
			return child
		}
	}
	return nil
}
