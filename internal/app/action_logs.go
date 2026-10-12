// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) runLogsCommand() *cobra.Command {
	var output string
	var selected int64
	var follow bool
	var interval, timeout time.Duration
	cmd := &cobra.Command{Use: "logs <run-id>", Short: "Stream logs for a run or a selected job", Annotations: textOutput("run logs emits text"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		if interval <= 0 || timeout <= 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--interval and --timeout must be positive"))
		}
		run, err := number(args[0])
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("job") && selected < 1 {
			return clierr.Wrap(clierr.Usage, errors.New("--job must be a positive ID"))
		}
		if output != "" && (follow || cmd.Flags().Changed("job")) {
			return clierr.Wrap(clierr.Usage, errors.New("--output-file downloads a run archive and cannot be combined with --job or --follow"))
		}
		if output != "" {
			if err := validateDownloadDestination(output); err != nil {
				return err
			}
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		service := services.Actions{Client: target.client, Repository: target.Repo}
		if output != "" {
			body, err := service.RunLogsArchive(cmd.Context(), run)
			if err != nil {
				return err
			}
			defer body.Close()
			return writeDownload(cmd, body, output)
		}
		jobs, err := service.Jobs(cmd.Context(), run)
		if err != nil {
			return err
		}
		if selected != 0 {
			found := false
			for _, job := range jobs {
				if job.ID == selected {
					jobs = []services.Job{job}
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("job %d does not belong to run %d", selected, run)
			}
		}
		out := terminal.SafeWriter(cmd.OutOrStdout(), a.deps.Streams.OutTTY, true)
		if follow {
			if len(jobs) != 1 {
				return clierr.Wrap(clierr.Usage, errors.New("--follow requires exactly one job; select --job"))
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			return service.FollowJobLogs(ctx, run, jobs[0].ID, interval, func(chunk []byte) error {
				n, err := out.Write(chunk)
				if err == nil && n != len(chunk) {
					return io.ErrShortWrite
				}
				return err
			})
		}
		for _, job := range jobs {
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Job %d: %s\n", job.ID, terminal.SafeLine(job.Name)); err != nil {
				return err
			}
			logs, err := service.JobLogs(cmd.Context(), job.ID)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, logs)
			closeErr := logs.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}}
	cmd.Flags().StringVar(&output, "output-file", "", "Download run logs as ZIP to a new file, or - for stdout")
	cmd.Flags().Int64Var(&selected, "job", 0, "Only stream this job's logs (must belong to the run)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow newly appended logs until the selected job completes")
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Second, "Log polling interval")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Hour, "Maximum follow duration")
	return cmd
}
