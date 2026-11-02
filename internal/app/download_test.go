// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type failingDownload struct{}

func (failingDownload) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// umaskMode returns the mode the umask leaves of 0666, as for files a shell creates.
func umaskMode(t *testing.T) os.FileMode {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe")
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o666)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	info, err := os.Stat(probe)
	require.NoError(t, err)
	return info.Mode().Perm()
}

func TestDownloadsPublishOnlyCompleteFiles(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	copyBody := func(body io.Reader) func(io.Writer) error {
		return func(destination io.Writer) error { _, err := io.Copy(destination, body); return err }
	}
	require.NoError(t, writeTerminalSafe(cmd.OutOrStdout(), false, bytes.NewBufferString("archive")))
	require.Equal(t, "archive", stdout.String())
	directory := t.TempDir()
	output := filepath.Join(directory, "logs.zip")
	require.ErrorIs(t, publish(cmd, output, false, false, copyBody(failingDownload{})), io.ErrUnexpectedEOF)
	_, err := os.Stat(output)
	require.True(t, errors.Is(err, os.ErrNotExist))
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, publish(cmd, output, false, false, copyBody(bytes.NewBufferString("archive"))))
	info, err := os.Stat(output)
	require.NoError(t, err)
	require.Equal(t, umaskMode(t), info.Mode().Perm(), "downloads are not private files")
	require.ErrorContains(t, publish(cmd, output, false, false, copyBody(bytes.NewBufferString("replacement"))), "already exists")
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "archive", string(data))
	entries, err = os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	private := filepath.Join(directory, "response.json")
	require.NoError(t, writeOutput(cmd, private, func(w io.Writer) error { _, err := io.WriteString(w, "{}"); return err }))
	info, err = os.Stat(private)
	require.NoError(t, err)
	if runtime.GOOS != "windows" { // Windows has no permission bits to keep private
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "API responses stay private")
	}
	missing := filepath.Join(directory, "missing", "x.txt")
	err = writeOutput(cmd, missing, func(w io.Writer) error { return nil })
	require.ErrorIs(t, err, os.ErrNotExist)
	require.True(t, strings.HasPrefix(err.Error(), "cannot write "+missing+": "), err.Error())
	require.NotContains(t, err.Error(), ".gt-download")
}

func TestDownloadDestinations(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	require.NoError(t, os.Mkdir("dl", 0o755))
	require.NoError(t, os.WriteFile("existing.txt", []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join("dl", "existing.txt"), []byte("old"), 0o644))
	for _, tc := range []struct {
		name  string
		flags downloadFlags
		file  string
		want  string
		skip  bool
		err   string
	}{
		{name: "default name", file: "notes.txt", want: "notes.txt"},
		{name: "--dir", flags: downloadFlags{dir: "dl"}, file: "notes.txt", want: filepath.Join("dl", "notes.txt")},
		{name: "--output file", flags: downloadFlags{output: "renamed.txt"}, file: "notes.txt", want: "renamed.txt"},
		{name: "--output directory", flags: downloadFlags{output: "dl"}, file: "notes.txt", want: filepath.Join("dl", "notes.txt")},
		{name: "--output directory with slash", flags: downloadFlags{output: "dl/"}, file: "notes.txt", want: filepath.Join("dl", "notes.txt")},
		{name: "stdout", flags: downloadFlags{output: "-"}, file: "notes.txt", want: "-"},
		{name: "existing", file: "existing.txt", err: "existing.txt already exists; pass --clobber to replace it or --skip-existing to keep it"},
		{name: "existing in --dir", flags: downloadFlags{dir: "dl"}, file: "existing.txt", err: filepath.Join("dl", "existing.txt") + " already exists"},
		{name: "--clobber", flags: downloadFlags{clobber: true}, file: "existing.txt", want: "existing.txt"},
		{name: "--skip-existing", flags: downloadFlags{skipExisting: true}, file: "existing.txt", want: "existing.txt", skip: true},
		{name: "parent directory name", file: "../escape.txt", err: `cannot use "../escape.txt" as a file name; pass --output`},
		{name: "dot dot", file: "..", err: "cannot use"},
		{name: "escape sequence in the name", file: "\x1b]0;PWNED\x07.txt", err: "cannot use"},
		{name: "bidi override in the name", file: "a\u202etxt.exe", err: "cannot use"},
		{name: "a file named dash stays a file", file: "-", want: "." + string(filepath.Separator) + "-"},
		{name: "--output and --dir", flags: downloadFlags{output: "x", dir: "dl"}, file: "notes.txt", err: "--output cannot be combined with --dir"},
		{name: "--clobber and --skip-existing", flags: downloadFlags{clobber: true, skipExisting: true}, file: "notes.txt", err: "--clobber cannot be combined with --skip-existing"},
		{name: "--clobber to standard output", flags: downloadFlags{output: "-", clobber: true}, file: "notes.txt", err: "--clobber cannot be combined with --output -"},
		{name: "--skip-existing to standard output", flags: downloadFlags{output: "-", skipExisting: true}, file: "notes.txt", err: "--skip-existing cannot be combined with --output -"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, skip, err := tc.flags.destination(tc.file)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				require.Equal(t, clierr.Usage, clierr.ExitCode(err))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, path)
			require.Equal(t, tc.skip, skip)
		})
	}
}

func TestDownloadClobberReplacesTheFileCompletely(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("asset.bin", []byte("old"), 0o600))
	deps, _, _ := testDeps(t)
	a := &application{deps: deps}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	flags := downloadFlags{clobber: true}
	path, _, err := flags.destination("asset.bin")
	require.NoError(t, err)
	require.NoError(t, a.download(cmd, flags, path, strings.NewReader("new")))
	data, err := os.ReadFile("asset.bin")
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
	info, err := os.Stat("asset.bin")
	require.NoError(t, err)
	require.Equal(t, umaskMode(t), info.Mode().Perm())
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file is left behind")
}

func TestDownloadsToATerminalRefuseBinaryData(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		tty              bool
		err              string
	}{
		{name: "piped binary stays byte exact", body: "PK\x03\x04\x00\x01", want: "PK\x03\x04\x00\x01"},
		{name: "terminal text loses escapes", body: "title\x1b]0;PWNED\x07\n", want: "title]0;PWNED\n", tty: true},
		{name: "terminal binary", body: "PK\x03\x04\x00\x01", tty: true, err: "refusing to write binary data to the terminal"},
		{name: "terminal invalid UTF-8", body: "abc\xff\xfedef", tty: true, err: "refusing to write binary data"},
		{name: "terminal 3-byte rune cut after 2 bytes", body: strings.Repeat("a", binarySniffLength-2) + "\u2026", want: strings.Repeat("a", binarySniffLength-2) + "\u2026", tty: true},
		{name: "terminal 4-byte rune cut after 2 bytes", body: strings.Repeat("a", binarySniffLength-2) + "\U0001F600", want: strings.Repeat("a", binarySniffLength-2) + "\U0001F600", tty: true},
		{name: "terminal UTF-8 cut at the sniff boundary", body: strings.Repeat("a", binarySniffLength-1) + "é", want: strings.Repeat("a", binarySniffLength-1) + "é", tty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := testDeps(t)
			deps.Streams = terminal.Streams{Out: out, OutTTY: tc.tty}
			a := &application{deps: deps}
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			cmd.SetOut(out)
			err := a.download(cmd, downloadFlags{output: "-"}, "-", strings.NewReader(tc.body))
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				require.Empty(t, out.String())
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestDownloadFlagsRegisterGhNames(t *testing.T) {
	var flags downloadFlags
	cmd := &cobra.Command{}
	flags.register(cmd)
	require.NoError(t, cmd.Flags().Parse([]string{"-O", "file", "--clobber"}))
	require.Equal(t, downloadFlags{output: "file", clobber: true}, flags)
	require.NoError(t, cmd.Flags().Parse([]string{"--output", "other", "-D", "dir", "--skip-existing"}))
	require.Equal(t, "other", flags.output)
	require.Equal(t, "dir", flags.dir)
}

func TestDownloadCreatesTheMissingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	deps, _, _ := testDeps(t)
	a := &application{deps: deps}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	flags := downloadFlags{dir: filepath.Join("dist", "assets")}
	path, _, err := flags.destination("asset.bin")
	require.NoError(t, err)
	require.NoError(t, a.download(cmd, flags, path, strings.NewReader("new")))
	data, err := os.ReadFile(filepath.Join("dist", "assets", "asset.bin"))
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
}

func TestCopyExclusiveStandsInForFilesystemsWithoutHardLinks(t *testing.T) {
	directory := t.TempDir()
	temporary := filepath.Join(directory, ".gt-download-1")
	require.NoError(t, os.WriteFile(temporary, []byte("body"), 0o600))
	output := filepath.Join(directory, "asset.bin")
	linkErr := errors.New("operation not supported")
	require.NoError(t, copyExclusive(temporary, output, true, linkErr))
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "body", string(data))
	require.ErrorContains(t, copyExclusive(temporary, output, true, linkErr), "already exists")
	require.ErrorIs(t, copyExclusive(temporary, filepath.Join(directory, "missing", "asset.bin"), true, linkErr), linkErr)
}
