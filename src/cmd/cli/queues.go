package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"AutoAnimeDownloader/src/internal/api"

	"github.com/urfave/cli/v2"
)

func queuesCommand() *cli.Command {
	return &cli.Command{
		Name:  "queues",
		Usage: "List and manage the download queues",
		Description: "Queues search in their order: a later queue's searches wait for the earlier queues'.\n" +
			"Downloads share max_concurrent_downloads, whatever their queue. Each queue ranks releases\n" +
			"with its own priorities (the ignore list is shared). Every anime is in one queue: the\n" +
			"default one until it is moved (`animes queue`, or `animes add --queue`). The default\n" +
			"queue can't be deleted; its priorities are the ones `priorities get` shows. With no\n" +
			"subcommand, lists the queues.",
		Action: handleQueuesList,
		Subcommands: []*cli.Command{
			{Name: "list", Usage: "List the queues in their order", Action: handleQueuesList},
			{
				Name:      "add",
				Usage:     "Make a queue at the end of the order, ranking as a copy of the default queue",
				ArgsUsage: "<name>",
				Action:    handleQueuesAdd,
			},
			{Name: "rename", Usage: "Rename a queue (its id stays)", ArgsUsage: "<id> <name>", Action: handleQueuesRename},
			{
				Name:        "order",
				Usage:       "Set the queues' order",
				ArgsUsage:   "<id> <id>...",
				Description: "Name every queue's id once, the first to search first. `queues` shows the ids.",
				Action:      handleQueuesOrder,
			},
			{
				Name:      "delete",
				Usage:     "Delete a queue; its anime go back to the default queue",
				ArgsUsage: "<id>",
				Flags:     []cli.Flag{yesFlag()},
				Action:    handleQueuesDelete,
			},
			{
				Name:  "priorities",
				Usage: "Show or set a queue's ranking",
				Subcommands: []*cli.Command{
					{Name: "get", Usage: "Show a queue's ranking", ArgsUsage: "<id>", Action: handleQueuePrioritiesGet},
					{
						Name:      "set",
						Usage:     "Set a queue's ranking from a JSON file (- for stdin)",
						ArgsUsage: "<id> <file|->",
						Description: "The lists the JSON names replace the queue's; the others keep their value. Start\n" +
							"from `queues priorities get <id> --json`. For the default queue this writes the\n" +
							"config's priorities, as `priorities set` does.",
						Action: handleQueuePrioritiesSet,
					},
				},
			},
		},
	}
}

func handleQueuesList(c *cli.Context) error {
	queues, err := getClient().GetQueues()
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(queues)
		return nil
	}
	type row struct {
		Order  int    `json:"order"`
		ID     string `json:"id"`
		Name   string `json:"name"`
		Animes string `json:"animes"`
	}
	rows := make([]row, 0, len(queues))
	for i, q := range queues {
		animes := fmt.Sprint(len(q.Animes))
		if q.Default {
			animes = "every other anime"
		}
		rows = append(rows, row{Order: i + 1, ID: q.ID, Name: q.Name, Animes: animes})
	}
	return writeSliceTable(rows, "No queues")
}

func handleQueuesAdd(c *cli.Context) error {
	if err := requireArgs(c, 1, "queues add <name>"); err != nil {
		return err
	}
	q, err := getClient().CreateQueue(c.Args().Get(0))
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(q)
		return nil
	}
	fmt.Printf("Queue %q made with id %s\n", q.Name, q.ID)
	return nil
}

func handleQueuesRename(c *cli.Context) error {
	if err := requireArgs(c, 2, "queues rename <id> <name>"); err != nil {
		return err
	}
	if err := mapClientError(getClient().RenameQueue(c.Args().Get(0), c.Args().Get(1))); err != nil {
		return err
	}
	return printMessage("Queue renamed", "Queue renamed")
}

func handleQueuesOrder(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: queues order <id> <id>...")
	}
	if err := mapClientError(getClient().OrderQueues(c.Args().Slice())); err != nil {
		return err
	}
	return printMessage("Queue order: "+strings.Join(c.Args().Slice(), ", "), "Queue order saved")
}

func handleQueuesDelete(c *cli.Context) error {
	if err := requireArgs(c, 1, "queues delete <id>"); err != nil {
		return err
	}
	if err := requireYes(c); err != nil {
		return err
	}
	moved, err := getClient().DeleteQueue(c.Args().Get(0))
	if err != nil {
		return mapClientError(err)
	}
	return printMessage(fmt.Sprintf("Queue deleted; %d anime went back to the default queue", moved), "Queue deleted")
}

func queueByID(id string) (*api.Queue, error) {
	queues, err := getClient().GetQueues()
	if err != nil {
		return nil, mapClientError(err)
	}
	for _, q := range queues {
		if q.ID == id {
			return &q, nil
		}
	}
	return nil, fmt.Errorf("no queue has the id %q (see `queues`)", id)
}

func handleQueuePrioritiesGet(c *cli.Context) error {
	if err := requireArgs(c, 1, "queues priorities get <id>"); err != nil {
		return err
	}
	q, err := queueByID(c.Args().Get(0))
	if err != nil {
		return err
	}
	return printValue(q.Priorities, "")
}

func handleQueuePrioritiesSet(c *cli.Context) error {
	if err := requireArgs(c, 2, "queues priorities set <id> <file|->"); err != nil {
		return err
	}
	var body json.RawMessage
	if err := readJSONArg(c.Args().Get(1), &body); err != nil {
		return err
	}
	if err := mapClientError(getClient().UpdateQueuePriorities(c.Args().Get(0), body)); err != nil {
		return err
	}
	return printMessage("Queue ranking updated", "Queue ranking updated")
}
