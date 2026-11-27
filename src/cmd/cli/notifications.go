package main

import (
	"fmt"

	"AutoAnimeDownloader/src/internal/files"

	"github.com/urfave/cli/v2"
)

func notificationsCommand() *cli.Command {
	return &cli.Command{
		Name:  "notifications",
		Usage: "Notification helpers",
		Subcommands: []*cli.Command{
			{
				Name:      "test",
				Usage:     "Fire a test payload to a named webhook",
				ArgsUsage: "<name>",
				Action:    handleNotificationsTest,
			},
			{
				Name:      "preview",
				Usage:     "Show what a named webhook would send, without sending it",
				ArgsUsage: "<name>",
				Description: "Renders the saved preset with the same sample values as `notifications test`\n" +
					"(method, URL, headers and body after {{variable}} substitution).",
				Action: handleNotificationsPreview,
			},
			{
				Name:  "get",
				Usage: "Show the saved notification settings (webhooks and batch window)",
				Action: func(c *cli.Context) error {
					return handleNotificationsGet()
				},
			},
			{
				Name:      "set",
				Usage:     "Save the notification settings from a JSON file (- for stdin), and nothing else",
				ArgsUsage: "<file|->",
				Description: "Writes only `notifications` (PUT /config/notifications), as the Notifications page does:\n" +
					"it doesn't run the folder and torrent-client checks of a full `config set`. Start from\n" +
					"`notifications get --json`.",
				Action: handleNotificationsSet,
			},
		},
	}
}

func handleNotificationsTest(c *cli.Context) error {
	if err := requireArgs(c, 1, "notifications test <name>"); err != nil {
		return err
	}
	if err := mapClientError(getClient().TestWebhook(c.Args().Get(0))); err != nil {
		return err
	}
	return printMessage("Webhook fired successfully", "Webhook fired successfully")
}

func handleNotificationsPreview(c *cli.Context) error {
	if err := requireArgs(c, 1, "notifications preview <name>"); err != nil {
		return err
	}
	name := c.Args().Get(0)
	client := getClient()
	config, err := client.GetConfig()
	if err != nil {
		return mapClientError(err)
	}
	preset, ok := findWebhook(config.Notifications.Webhooks, name)
	if !ok {
		return fmt.Errorf("webhook not found: %q", name)
	}
	rendered, err := client.PreviewWebhook(preset)
	if err != nil {
		return mapClientError(err)
	}
	return printValue(rendered, "")
}

func findWebhook(webhooks []files.WebhookPreset, name string) (files.WebhookPreset, bool) {
	for _, w := range webhooks {
		if w.Name == name {
			return w, true
		}
	}
	return files.WebhookPreset{}, false
}

func handleNotificationsGet() error {
	config, err := getClient().GetConfig()
	if err != nil {
		return mapClientError(err)
	}
	return printValue(config.Notifications, "")
}

func handleNotificationsSet(c *cli.Context) error {
	if err := requireArgs(c, 1, "notifications set <file|->"); err != nil {
		return err
	}
	var notifications files.NotificationsConfig
	if err := readJSONArg(c.Args().Get(0), &notifications); err != nil {
		return err
	}
	if err := mapClientError(getClient().UpdateNotifications(notifications)); err != nil {
		return err
	}
	return printMessage("Notifications updated", "Notifications updated")
}
