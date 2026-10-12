// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) runLogsCommand() *cobra.Command {
	var archive downloadFlags
	var job string
	var follow, failed bool
	var interval, timeout time.Duration
	cmd := &cobra.Command{
		Use:   "logs [<run-id>]",
		Short: "Print the logs of a run's jobs",
		Long: `Print the logs of a run's jobs, one after another; the name of each job goes to stderr.

With --follow, gt waits for jobs to start and prints new log lines until they complete,
prefixing each line with its job's name when it follows several jobs. With --output or --dir,
it downloads the logs of the whole run as a ZIP archive instead.`,
		Example: `# print the logs of every job of a run
$ gt run logs 34
# follow the logs of one job while it runs
$ gt run logs 34 --job build --follow
# download the logs as a ZIP archive
$ gt run logs 34 --output logs.zip`,
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		Annotations: map[string]string{"gt.arguments": runArguments, "gt.text": "run logs emits text"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval <= 0 || timeout <= 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--interval and --timeout must be positive"))
			}
			download := archive.output != "" || archive.dir != ""
			if download && (follow || failed || job != "") {
				return clierr.Wrap(clierr.Usage, errors.New("--output and --dir download the whole run and cannot be combined with --job, --failed or --follow"))
			}
			if follow && failed {
				return clierr.Wrap(clierr.Usage, errors.New("--follow and --failed cannot be combined"))
			}
			t, run, err := a.runArg(cmd, args, false)
			if err != nil {
				return err
			}
			service := services.Actions{Client: t.client, Repository: t.Repo}
			if download {
				path, skip, err := archive.destination(fmt.Sprintf("run-%d-logs.zip", run))
				if err != nil || skip {
					return err
				}
				body, err := service.RunLogsArchive(cmd.Context(), run)
				if err != nil {
					return err
				}
				defer body.Close()
				if err := a.download(cmd, archive, path, body); err != nil {
					return err
				}
				if path != "-" {
					a.done(cmd, "Downloaded the logs of run %d to %s", run, path)
				}
				return nil
			}
			jobs, err := service.Jobs(cmd.Context(), run)
			if err != nil {
				return err
			}
			switch {
			case job != "":
				selected, err := findJob(jobs, job, run)
				if err != nil {
					return err
				}
				jobs = []services.Job{selected}
			case failed:
				jobs = slices.DeleteFunc(jobs, func(job services.Job) bool { return job.Conclusion != "failure" })
				if len(jobs) == 0 {
					return fmt.Errorf("run %d has no failed jobs", run)
				}
			}
			if !follow {
				return a.printJobLogs(cmd, service, jobs)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			return timeoutError(a.followJobLogs(ctx, cmd, service, run, jobs, interval), timeout, "following the logs of run %d", run)
		},
	}
	archive.register(cmd)
	cmd.Flags().StringVarP(&job, "job", "j", "", "Only the logs of this job, by `name or ID`")
	cmd.Flags().BoolVar(&failed, "failed", false, "Only the logs of failed jobs")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Wait for jobs to start and print new lines until they complete")
	cmd.Flags().DurationVarP(&interval, "interval", "i", 5*time.Second, "Polling interval with --follow")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Hour, "Maximum time to follow")
	cmd.ValidArgsFunction = firstArg(a.completeRuns())
	_ = cmd.RegisterFlagCompletionFunc("job", a.completeJobs())
	return cmd
}

// printJobLogs prints the logs of jobs on stdout, each headed by its name on stderr. Jobs that
// have not started are reported on stderr and skipped. A terminal keeps only the colors of logs.
func (a *application) printJobLogs(cmd *cobra.Command, service services.Actions, jobs []services.Job) error {
	out := terminal.SafeWriter(cmd.OutOrStdout(), a.deps.Streams.OutTTY, true)
	for _, job := range jobs {
		label := fmt.Sprintf("%s (job %d)", terminal.SafeLine(job.Name), job.ID)
		var logs io.ReadCloser
		var err error
		if job.Started() {
			logs, err = service.JobLogs(cmd.Context(), job.ID)
		}
		if !job.Started() || errors.Is(err, services.ErrJobNotStarted) {
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", label, unstarted(job)); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), label); err != nil {
			_ = logs.Close()
			return err
		}
		_, copyErr := io.Copy(out, logs)
		closeErr := logs.Close()
		if err := cmp.Or(copyErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}

// followJobLogs follows the logs of jobs one after another until each completes, waiting for
// those that have not started. Following several jobs prefixes each line with its job's name.
func (a *application) followJobLogs(ctx context.Context, cmd *cobra.Command, service services.Actions, run int64, jobs []services.Job, interval time.Duration) error {
	out := terminal.SafeWriter(cmd.OutOrStdout(), a.deps.Streams.OutTTY, true)
	for _, job := range jobs {
		label := fmt.Sprintf("%s (job %d)", terminal.SafeLine(job.Name), job.ID)
		if job.Started() {
			fmt.Fprintln(cmd.ErrOrStderr(), label)
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: waiting for it to start…\n", label)
		}
		lines := &prefixWriter{w: out}
		if len(jobs) > 1 {
			lines.prefix = []byte(terminal.SafeLine(job.Name) + "\t")
		}
		err := service.FollowJobLogs(ctx, run, job.ID, interval, func(chunk []byte) error {
			_, err := lines.Write(chunk)
			return err
		})
		if errors.Is(err, services.ErrJobNotStarted) {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: never started\n", label)
			continue
		}
		if err := cmp.Or(err, lines.Flush()); err != nil {
			return err
		}
	}
	return nil
}

// prefixWriter writes prefix before every line it passes to w; Flush ends a last unterminated line.
type prefixWriter struct {
	w       io.Writer
	prefix  []byte
	partial bool
}

func (p *prefixWriter) Write(data []byte) (int, error) {
	if len(p.prefix) == 0 {
		return p.w.Write(data)
	}
	var b bytes.Buffer
	for line := range bytes.Lines(data) {
		if !p.partial {
			b.Write(p.prefix)
		}
		b.Write(line)
		p.partial = !bytes.HasSuffix(line, []byte("\n"))
	}
	if _, err := p.w.Write(b.Bytes()); err != nil {
		return 0, err
	}
	return len(data), nil
}

// Flush terminates a last line that has no line break.
func (p *prefixWriter) Flush() error {
	if !p.partial {
		return nil
	}
	p.partial = false
	_, err := p.w.Write([]byte("\n"))
	return err
}

// unstarted explains why a job has no logs: it is waiting for a runner, or it completed, such
// as by cancellation, before one started it.
func unstarted(job services.Job) string {
	if job.CompletedAt == nil {
		return "not started yet"
	}
	return "never started (" + cmp.Or(job.Conclusion, job.Status) + ")"
}
