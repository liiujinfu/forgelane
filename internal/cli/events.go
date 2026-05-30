package cli

import (
	"fmt"
	"io"

	"github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/spf13/cobra"
)

func newEventsCommand(stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Inspect ForgeLane audit Events.",
	}
	cmd.AddCommand(newEventsListCommand(stdout))
	return cmd
}

func newEventsListCommand(stdout io.Writer) *cobra.Command {
	var runIDInput string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List audit Events.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if runIDInput == "" {
				return fmt.Errorf("pass --run <run_id>")
			}
			runID, err := parseAgentRunID(runIDInput)
			if err != nil {
				return err
			}

			instanceStore, err := openReadOnlyStore()
			if err != nil {
				return err
			}
			defer instanceStore.Close()

			events, err := instanceStore.ListEventsForAgentRun(runID)
			if err != nil {
				return err
			}
			printRunEvents(stdout, runID, events)
			return nil
		},
	}
	cmd.Flags().StringVar(&runIDInput, "run", "", "AgentRun id")
	return cmd
}

func printRunEvents(stdout io.Writer, runID int64, events []sqlite.Event) {
	fmt.Fprintf(stdout, "Events for AgentRun %d\n", runID)
	for _, event := range events {
		fmt.Fprintf(stdout, "Event %d: %s\n", event.ID, event.Type)
		fmt.Fprintf(stdout, "Occurred: %s\n", event.OccurredAt)
		fmt.Fprintf(stdout, "Actor: %s\n", event.Actor)
		fmt.Fprintf(stdout, "Subject: %s %s\n", event.SubjectType, event.SubjectRef)
	}
}
