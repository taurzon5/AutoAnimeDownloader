package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
)

func shokoCommand() *cli.Command {
	return &cli.Command{
		Name:  "shoko",
		Usage: "Sign in to Shoko, see what the app read from it, sync it",
		Subcommands: []*cli.Command{
			{
				Name:  "login",
				Usage: "Sign in to Shoko; only the API key Shoko returns is saved",
				Description: "Reads the password from standard input (piped, or typed after the prompt, where it\n" +
					"is shown as you type). Shoko returns an API key for the app; the password is kept nowhere.",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "url", Usage: "Shoko's address, e.g. http://127.0.0.1:8111", Required: true},
					&cli.StringFlag{Name: "user", Usage: "Shoko username", Required: true},
				},
				Action: handleShokoLogin,
			},
			{
				Name:   "status",
				Usage:  "Show whether Shoko is on and signed in, and what the last sync read",
				Action: handleShokoStatus,
			},
			{
				Name:  "sync",
				Usage: "Start a check pass that syncs with Shoko first",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "full", Usage: "Read all of Shoko again (files deleted or moved in Shoko show up)"},
				},
				Action: handleShokoSync,
			},
		},
	}
}

func handleShokoLogin(c *cli.Context) error {
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "Shoko password (shown as you type): ")
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("no password on standard input")
	}
	password := strings.TrimRight(line, "\r\n")
	if err := mapClientError(getClient().ShokoLogin(c.String("url"), c.String("user"), password)); err != nil {
		return err
	}
	return printMessage("Signed in to Shoko", "Signed in to Shoko")
}

func handleShokoStatus(c *cli.Context) error {
	st, err := getClient().ShokoStatus()
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(st)
		return nil
	}
	fmt.Printf("Shoko: %s, %s\n", map[bool]string{true: "on", false: "off"}[st.Enabled],
		map[bool]string{true: "signed in to " + st.URL, false: "not signed in"}[st.SignedIn])
	if st.LastSync.IsZero() {
		fmt.Println("Never synced")
	} else {
		fmt.Printf("Last sync %s (full %s): %d series, %d episodes of followed anime\n",
			st.LastSync.Local().Format(time.DateTime), st.LastFullSync.Local().Format(time.DateTime), st.Series, st.OwnedEpisodes)
	}
	if st.LastError != "" {
		fmt.Printf("Last error: %s\n", st.LastError)
	}
	switch {
	case st.Watching:
		fmt.Println("Following Shoko's changes as they happen")
	case st.WatchError != "":
		fmt.Printf("Not following Shoko's changes (retrying): %s\n", st.WatchError)
	}
	return nil
}

func handleShokoSync(c *cli.Context) error {
	if err := mapClientError(getClient().ShokoSync(c.Bool("full"))); err != nil {
		return err
	}
	return printMessage("Shoko sync started with a check pass", "Shoko sync started")
}
