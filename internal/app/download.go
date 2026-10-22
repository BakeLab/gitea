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
	"unicode"
	"unicode/utf8"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

type downloadFlags struct {
	// output is a file, an existing directory to download into, or - for standard output.
	output string
	// dir is the directory that receives the download under its own name.
	dir string
	// clobber replaces an existing file; skipExisting keeps it and skips the download.
	clobber, skipExisting bool
}

func (f *downloadFlags) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&f.output, "output", "O", "", "File or directory to write to, or - for standard output (default: the download's name)")
	flags.StringVarP(&f.dir, "dir", "D", "", "Directory to download into")
	flags.BoolVar(&f.clobber, "clobber", false, "Replace an existing file")
	flags.BoolVar(&f.skipExisting, "skip-existing", false, "Keep an existing file and skip the download")
}

// validate rejects combinations of the download flags that cannot work.
func (f downloadFlags) validate() error {
	switch {
	case f.output != "" && f.dir != "":
		return clierr.Wrap(clierr.Usage, errors.New("--output cannot be combined with --dir"))
	case f.clobber && f.skipExisting:
		return clierr.Wrap(clierr.Usage, errors.New("--clobber cannot be combined with --skip-existing"))
	case f.output == "-" && f.clobber:
		return clierr.Wrap(clierr.Usage, errors.New("--clobber cannot be combined with --output -"))
	case f.output == "-" && f.skipExisting:
		return clierr.Wrap(clierr.Usage, errors.New("--skip-existing cannot be combined with --output -"))
	}
	return nil
}

// destination returns where a download named name goes, or "-" for standard output. It fails
// before anything is downloaded when the file exists without --clobber or --skip-existing.
func (f downloadFlags) destination(name string) (path string, skip bool, err error) {
	if err := f.validate(); err != nil {
		return "", false, err
	}
	if f.output == "-" {
		return "-", false, nil
	}
	path = f.output
	if path == "" || isDirectory(path) {
		if !isFileName(name) {
			return "", false, clierr.Wrap(clierr.Usage, fmt.Errorf("cannot use %q as a file name; pass --output", name))
		}
		path = filepath.Join(cmp.Or(path, f.dir, "."), name)
		if path == "-" {
			path = "." + string(filepath.Separator) + path // a file named "-", not standard output
		}
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

// isFileName reports whether name can be saved under its own name in a directory: a single
// path element other than "." or "..", without control or invisible format characters.
func isFileName(name string) bool {
	return name != "." && filepath.IsLocal(name) && !strings.ContainsAny(name, `/\`) &&
		!strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
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
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	return publish(cmd, path, false, f.clobber, func(destination io.Writer) error {
		_, err := io.Copy(destination, body)
		return err
	})
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
		if !utf8.FullRune(data) {
			return false
		}
		r, size := utf8.DecodeRune(data)
		if r == 0 || (r == utf8.RuneError && size == 1) {
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
	if err != nil {
		// Some filesystems, such as FAT and many network mounts, cannot hard link.
		return copyExclusive(file.Name(), output, private, err)
	}
	return nil
}

// copyExclusive copies the finished temporary file to output, which must not exist, when linking
// failed with linkErr. A failed copy removes the partial output.
func copyExclusive(temporary, output string, private bool, linkErr error) error {
	mode := os.FileMode(0o666)
	if private {
		mode = 0o600
	}
	destination, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s already exists", output))
	}
	if err != nil {
		return linkErr
	}
	source, err := os.Open(temporary)
	if err == nil {
		defer source.Close()
		_, err = io.Copy(destination, source)
	}
	if closeErr := destination.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(output)
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
