// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// downloadFlags are the destination flags of a download command.
type downloadFlags struct {
	// output is a file, an existing directory to download into, or - for standard output.
	output string
	// dir is the directory that receives the download under its own name.
	dir string
	// clobber replaces an existing file; skipExisting keeps it and skips the download.
	clobber, skipExisting bool
}

// register adds -O/--output (and its older name --output-file), -D/--dir, --clobber and
// --skip-existing to cmd.
func (f *downloadFlags) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&f.output, "output", "O", "", "File or directory to write to, or - for standard output (default: the download's name)")
	flags.StringVar(&f.output, "output-file", "", "Alias of --output")
	_ = flags.MarkHidden("output-file")
	flags.StringVarP(&f.dir, "dir", "D", "", "Directory to download into")
	flags.BoolVar(&f.clobber, "clobber", false, "Replace an existing file")
	flags.BoolVar(&f.skipExisting, "skip-existing", false, "Keep an existing file and skip the download")
}

// destination returns the path a download named name is written to: --output (inside it when it
// is a directory), else name inside --dir or the working directory, or "-" for standard
// output. skip reports an existing file that --skip-existing keeps. It fails before anything
// is downloaded when the file exists without --clobber or --skip-existing.
func (f downloadFlags) destination(name string) (path string, skip bool, err error) {
	switch {
	case f.output != "" && f.dir != "":
		return "", false, clierr.Wrap(clierr.Usage, errors.New("--output and --dir cannot be combined"))
	case f.clobber && f.skipExisting:
		return "", false, clierr.Wrap(clierr.Usage, errors.New("--clobber and --skip-existing cannot be combined"))
	case f.output == "-":
		return "-", false, nil
	}
	path = f.output
	if path == "" || isDirectory(path) {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			return "", false, clierr.Wrap(clierr.Usage, fmt.Errorf("cannot use %q as a file name; pass --output", name))
		}
		path = filepath.Join(cmp.Or(path, f.dir, "."), name)
	}
	if _, err := os.Lstat(path); err == nil {
		if f.skipExisting {
			return path, true, nil
		}
		if !f.clobber {
			return "", false, clierr.Wrap(clierr.Usage, fmt.Errorf("%s already exists; pass --clobber to replace it or --skip-existing to keep it", path))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	return path, false, nil
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return (err == nil && info.IsDir()) || strings.HasSuffix(path, string(filepath.Separator)) || strings.HasSuffix(path, "/")
}

// download writes body to path, a destination from downloadFlags.destination. A file is
// published complete with the permissions the umask allows; standard output refuses binary
// data on a terminal and strips terminal control sequences from text.
func (a *application) download(cmd *cobra.Command, f downloadFlags, path string, body io.Reader) error {
	if path == "-" {
		return writeTerminalSafe(cmd.OutOrStdout(), a.deps.Streams.OutTTY, body)
	}
	return publish(cmd, path, false, f.clobber, func(destination io.Writer) error {
		_, err := io.Copy(destination, body)
		return err
	})
}

func writeDownload(cmd *cobra.Command, body io.Reader, output string) error {
	if output == "" || output == "-" {
		return writeTerminalSafe(cmd.OutOrStdout(), isTerminal(cmd.OutOrStdout()), body)
	}
	return publish(cmd, output, false, false, func(destination io.Writer) error { _, err := io.Copy(destination, body); return err })
}

// isTerminal reports whether w is a terminal, for writers without injected stream capabilities.
func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

// binarySniffLength is how much of a download decides whether it is text.
const binarySniffLength = 8 << 10

// writeTerminalSafe copies body to w. On a terminal it refuses binary data, which would garble
// the terminal, and strips control sequences from text.
func writeTerminalSafe(w io.Writer, tty bool, body io.Reader) error {
	if !tty {
		_, err := io.Copy(w, body)
		return err
	}
	reader := bufio.NewReaderSize(body, binarySniffLength)
	head, err := reader.Peek(binarySniffLength)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return err
	}
	if binary(head) {
		return clierr.Wrap(clierr.Usage, errors.New("refusing to write binary data to the terminal; pass --output FILE or redirect standard output"))
	}
	_, err = io.Copy(terminal.SafeWriter(w, true, false), reader)
	return err
}

// binary reports data that is not UTF-8 text: a NUL byte or an invalid sequence other than a
// rune cut off at the end.
func binary(data []byte) bool {
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == 0 || (r == utf8.RuneError && size == 1 && (len(data) >= utf8.UTFMax || utf8.FullRune(data))) {
			return true
		}
		data = data[size:]
	}
	return false
}

// writeOutput writes to stdout when output is empty or -; otherwise it publishes a complete
// private file without replacing another writer.
func writeOutput(cmd *cobra.Command, output string, write func(io.Writer) error) error {
	if output == "" || output == "-" {
		return write(cmd.OutOrStdout())
	}
	return publish(cmd, output, true, false, write)
}

// publish writes a temporary file next to output and then links it into place, so output only
// ever holds a complete file. With replace it renames over an existing output; otherwise it
// never replaces a file created while it wrote. A private file is readable by its owner only;
// others get the permissions the umask allows, as files a shell redirection creates.
func publish(cmd *cobra.Command, output string, private, replace bool, write func(io.Writer) error) error {
	file, err := createTemp(filepath.Dir(output), private)
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	copyErr := write(file)
	closeErr := file.Close()
	if copyErr != nil {
		if cmd.Context().Err() != nil {
			return cmd.Context().Err()
		}
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if replace {
		return os.Rename(file.Name(), output)
	}
	// Linking never replaces a file created while the download ran.
	err = os.Link(file.Name(), output)
	if errors.Is(err, os.ErrExist) {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s already exists", output))
	}
	return err
}

// createTemp creates a new temporary file in dir. Unlike os.CreateTemp, which always creates
// mode 0600, a file that is not private gets 0666 less the umask.
func createTemp(dir string, private bool) (*os.File, error) {
	if private {
		return os.CreateTemp(dir, ".gt-download-*")
	}
	for range 100 {
		name := filepath.Join(dir, ".gt-download-"+strconv.FormatUint(rand.Uint64(), 36))
		file, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
		if !errors.Is(err, os.ErrExist) {
			return file, err
		}
	}
	return nil, errors.New("cannot create a temporary file in " + dir)
}

func validateDownloadDestination(output string) error {
	if output == "" {
		return clierr.Wrap(clierr.Usage, errors.New("supply --output-file, or --output-file - for stdout"))
	}
	if output == "-" {
		return nil
	}
	if isDirectory(output) {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s is a directory; pass a file path inside it", output))
	}
	if _, err := os.Lstat(output); err == nil {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s already exists", output))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
