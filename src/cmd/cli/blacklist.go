package main

import (
	"github.com/urfave/cli/v2"
)

// The app's blacklist (decisions/155): an anime on it is not checked or downloaded, its unfinished
// torrents are paused, and its files stay.
func blacklistCommand() *cli.Command {
	return &cli.Command{
		Name:  "blacklist",
		Usage: "List, add or remove anime on the app's blacklist",
		Description: "A blacklisted anime is no longer checked or downloaded and its unfinished torrents\n" +
			"are paused; its files stay. Taking it off resumes the torrents the blacklist paused.\n" +
			"Not the same as excluded_lists, which names AniList custom lists. With no subcommand,\n" +
			"lists the blacklist.",
		Action: handleBlacklistList,
		Subcommands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "List the blacklisted anime, newest first",
				Action: handleBlacklistList,
			},
			{
				Name:      "add",
				Usage:     "Blacklist an anime by AniList media id",
				ArgsUsage: "<id>",
				Action:    handleBlacklistAdd,
			},
			{
				Name:      "remove",
				Usage:     "Take an anime off the blacklist",
				ArgsUsage: "<id>",
				Action:    handleBlacklistRemove,
			},
		},
	}
}

func handleBlacklistList(c *cli.Context) error {
	entries, err := getClient().GetBlacklist()
	if err != nil {
		return mapClientError(err)
	}
	return printValue(entries, "The blacklist is empty")
}

func handleBlacklistAdd(c *cli.Context) error {
	if err := requireArgs(c, 1, "blacklist add <id>"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	if err := mapClientError(getClient().BlacklistAnime(id)); err != nil {
		return err
	}
	return printMessage("Anime blacklisted", "Anime blacklisted")
}

func handleBlacklistRemove(c *cli.Context) error {
	if err := requireArgs(c, 1, "blacklist remove <id>"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	if err := mapClientError(getClient().UnblacklistAnime(id)); err != nil {
		return err
	}
	return printMessage("Anime taken off the blacklist", "Anime taken off the blacklist")
}
