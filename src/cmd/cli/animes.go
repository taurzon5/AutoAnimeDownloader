package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"AutoAnimeDownloader/src/internal/api"
	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/urfave/cli/v2"
)

func animesCommand() *cli.Command {
	return &cli.Command{
		Name:  "animes",
		Usage: "List and manage animes",
		Description: "Without a subcommand, lists the tracked animes with the dashboard's filters and sort.\n" +
			"--account keeps the animes that account has in a download status and shows ITS progress\n" +
			"(episodes_watched / episodes_pending); with several accounts, the one furthest behind.",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{Name: "account", Usage: "Only this account's animes: `USER` or SOURCE:USER (anilist:alice, mal:alice); repeat for several"},
			&cli.StringFlag{Name: "name", Usage: "Only animes whose title (any of them: English, romaji, native, synonyms) contains `TEXT`"},
			&cli.BoolFlag{Name: "unwatched", Usage: "Hide animes whose released episodes are all watched (one with none released yet stays)"},
			&cli.StringFlag{Name: "type", Value: "all", Usage: "One of: " + strings.Join(animeTypes, ", ")},
			&cli.StringFlag{Name: "sort", Value: "last-download", Usage: "One of: name (A-Z), watched, last-download (both most first)"},
			&cli.BoolFlag{Name: "reverse", Usage: "Reverse the sort order"},
		},
		Action: handleAnimes,
		Subcommands: []*cli.Command{
			{
				Name:      "search",
				Usage:     "Search AniList for animes to add",
				ArgsUsage: "<query>",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "unreleased", Usage: "Include unreleased titles"},
				},
				Action: handleAnimesSearch,
			},
			{
				Name:      "candidates",
				Usage:     "List ranked whole-title torrent candidates for an anime",
				ArgsUsage: "<id>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "query", Usage: "Custom Nyaa search query"},
					&cli.StringFlag{Name: "queue", Usage: "Rank as this download queue would (default: the anime's own queue)"},
				},
				Action: handleAnimesCandidates,
			},
			{
				Name:      "episode-candidates",
				Usage:     "List ranked torrent candidates for one episode",
				ArgsUsage: "<id> <episode>",
				Description: "The single-episode picker: the pass's own search for the episode, or --query, with every\n" +
					"result listed (a hand pick ignores the size and seeder limits). Download one with\n" +
					"`animes replace-episode <id> <episode> --magnet <magnet>`.",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "query", Usage: "Custom Nyaa search query"},
				},
				Action: handleAnimesEpisodeCandidates,
			},
			{
				Name:      "candidate",
				Usage:     "Show the files and Nyaa page of one torrent candidate",
				ArgsUsage: "<id> <nyaa_id>",
				Description: "The detail panel of the web picker: the files inside the torrent and its Nyaa URL.\n" +
					"<nyaa_id> is the nyaa_id column of `animes candidates`. A megapack (files of two\n" +
					"or more AniList entries of the franchise) also gets its mapping table: which entry\n" +
					"each video belongs to, and the videos none takes. --json also carries the\n" +
					"description as HTML and the mapping of any torrent with two videos or more.",
				Action: handleAnimesCandidate,
			},
			{
				Name:      "add",
				Usage:     "Add a standalone anime by AniList media id",
				ArgsUsage: "<media_id>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "magnet", Usage: "Magnet URI (skips auto-pick)"},
					&cli.StringFlag{Name: "query", Usage: "With --magnet: the Nyaa query the magnet came from (same as candidates --query)"},
					&cli.BoolFlag{Name: "unblacklist", Usage: "The anime is on the blacklist: take it off and add it"},
					&cli.StringFlag{Name: "queue", Usage: "The download queue it goes in (a queue id from `queues`; default: the default queue)"},
					&cli.StringSliceFlag{Name: "skip-file", Usage: "With --magnet: a file of the torrent not to download, by its path in `animes candidate` (repeatable; qBittorrent only)"},
				},
				Action: handleAnimesAdd,
			},
			{
				Name:      "remove",
				Usage:     "Remove an anime added by hand, or delete a list anime's files or blacklist it",
				ArgsUsage: "<id>",
				Description: "An anime added by hand (standalone) is removed; --delete-episodes deletes its\n" +
					"downloaded episodes too, otherwise the files stay. An anime a list brings can't be\n" +
					"removed here, the list would bring it back: take it off the list on AniList or\n" +
					"MyAnimeList, or pass --delete-episodes (its episodes are deleted and won't download\n" +
					"again) and/or --blacklist. --blacklist stops the app from checking and downloading\n" +
					"the anime and pauses its unfinished torrents; its files stay. `blacklist remove`\n" +
					"lifts it.",
				Flags: []cli.Flag{
					yesFlag(),
					&cli.BoolFlag{Name: "delete-episodes", Usage: "Also delete downloaded episodes"},
					&cli.BoolFlag{Name: "blacklist", Usage: "Put the anime on the blacklist: no more checks or downloads"},
				},
				Action: handleAnimesRemove,
			},
			{
				Name:      "settings",
				Usage:     "Show or update anime settings",
				ArgsUsage: "<id> [progress <n> | shoko <series-id> [offset] | shoko none | shoko auto | shoko-packs download|remove|default | root <name> | root none]",
				Description: "With one argument, shows the anime's settings. progress sets a standalone anime's\n" +
					"progress. shoko sets its Shoko match by hand: a Shoko series id (and the offset, its\n" +
					"episode N being the series' N+offset), none for \"not in Shoko\", or auto to go back to\n" +
					"the match worked out from the MAL id. shoko-packs sets what a pack chosen for the missing\n" +
					"episodes does with the episodes it brings that are already in Shoko: download keeps them,\n" +
					"remove skips them (qBittorrent) or deletes them once downloaded, default follows the\n" +
					"config's shoko_full_packs. root sends the anime's new torrents to that download folder\n" +
					"first (direct mode with download_roots; a full folder falls through to the others);\n" +
					"none drops the override.",
				Action: handleAnimesSettings,
			},
			{
				Name:      "queue",
				Usage:     "Move an anime to a download queue",
				ArgsUsage: "<id> <queue-id>",
				Description: "The anime searches with that queue, in its turn, and ranks with its priorities.\n" +
					"`default` puts it back in the default queue. `queues` lists the ids.",
				Action: handleAnimesQueue,
			},
			{
				Name:      "debug",
				Usage:     "Run the search/match pipeline for one anime without downloading",
				ArgsUsage: "<id>",
				Description: "Same pass as the daemon's --debug-anime flag, run by the running daemon:\n" +
					"prints which episodes would be searched and how many magnets each found.\n" +
					"The full trace (AniList response, every Nyaa row, what matched) goes to the\n" +
					"daemon log — read it with `logs`. Can take minutes on a long series.",
				Action: handleAnimesDebug,
			},
			{
				Name:      "episodes",
				Usage:     "List episodes of an anime",
				ArgsUsage: "<id>",
				Action:    handleAnimesEpisodes,
			},
			{
				Name:      "releases",
				Usage:     "List the torrents that brought an anime's episodes",
				ArgsUsage: "<id>",
				Description: "One row per torrent: its release name, the episodes it brought, its state (downloading,\n" +
					"seeding, released, missing, removed), how it was added and the tags its name gives. A\n" +
					"torrent added before release details were kept shows \"before records\". --json gives\n" +
					"every field, including the Nyaa page, the seeders when chosen and the upload date.",
				Action: handleAnimesReleases,
			},
			{
				Name:      "download",
				Usage:     "Download an episode",
				ArgsUsage: "<id> <episode>",
				Action:    handleAnimesDownload,
			},
			{
				Name:      "redownload",
				Usage:     "Redownload one or more episodes",
				ArgsUsage: "<id> <episode> [<episode> ...]",
				Description: "With several episodes, one failing does not stop the others; the ones that\n" +
					"failed are listed and the command exits non-zero.",
				Flags:  []cli.Flag{yesFlag()},
				Action: handleAnimesRedownload,
			},
			{
				Name:      "delete",
				Usage:     "Delete one or more downloaded episodes",
				ArgsUsage: "<id> <episode> [<episode> ...]",
				Description: "Several episodes are deleted in one request, so a pack they share is freed.\n" +
					"If any of them is not downloaded, nothing is deleted.",
				Flags:  []cli.Flag{yesFlag()},
				Action: handleAnimesDelete,
			},
			{
				Name:      "release",
				Usage:     "Release an episode from manual management",
				ArgsUsage: "<id> <episode>",
				Action:    handleAnimesRelease,
			},
			{
				Name:      "replace",
				Usage:     "Replace an anime with a magnet",
				ArgsUsage: "<id>",
				Flags: []cli.Flag{
					yesFlag(),
					&cli.StringFlag{Name: "magnet", Usage: "Magnet URI"},
					&cli.StringSliceFlag{Name: "skip-file", Usage: "A file of the torrent not to download, by its path in `animes candidate` (repeatable; qBittorrent only)"},
				},
				Action: handleAnimesReplace,
			},
			{
				Name:      "replace-episode",
				Usage:     "Replace an episode with a magnet",
				ArgsUsage: "<id> <episode>",
				Flags: []cli.Flag{
					yesFlag(),
					&cli.StringFlag{Name: "magnet", Usage: "Magnet URI"},
				},
				Action: handleAnimesReplaceEpisode,
			},
		},
	}
}

func handleAnimes(c *cli.Context) error {
	view := animesView{
		name:      c.String("name"),
		unwatched: c.Bool("unwatched"),
		typ:       c.String("type"),
		reverse:   c.Bool("reverse"),
	}
	if !slices.Contains(animeTypes, view.typ) {
		return fmt.Errorf("invalid --type %q; one of: %s", view.typ, strings.Join(animeTypes, ", "))
	}
	sortKey, ok := animeSortKeys[c.String("sort")]
	if !ok {
		return fmt.Errorf("invalid --sort %q; one of: name, watched, last-download", c.String("sort"))
	}
	view.sortKey = sortKey

	client := getClient()
	if values := c.StringSlice("account"); len(values) > 0 {
		config, err := client.GetConfig()
		if err != nil {
			return mapClientError(err)
		}
		view.accounts, err = resolveAccounts(values, configuredAccounts(config.AnilistUsernames, config.MALUsernames))
		if err != nil {
			return err
		}
	}

	animes, failed, err := client.GetAnimes()
	if err != nil {
		return mapClientError(err)
	}
	// A failed account carries no anime, so filtering by it would print an empty list that reads
	// as "this account has nothing". That is an error; without that account picked it's a warning.
	var failedPicked []string
	for _, key := range failed {
		if slices.Contains(view.accounts, key) {
			failedPicked = append(failedPicked, key)
		}
	}
	if len(failedPicked) > 0 {
		return &cliError{exitCode: 1, code: "ACCOUNT_LIST_UNAVAILABLE", message: fmt.Sprintf(
			"the list of %s could not be fetched (AniList/MAL down or rate-limited); try again later",
			strings.Join(failedPicked, ", "))}
	}
	if len(failed) > 0 && !outputJSON {
		fmt.Fprintf(os.Stderr, "Warning: the list of %s could not be fetched; its animes with nothing downloaded are missing\n",
			strings.Join(failed, ", "))
	}
	animes = filterAnimes(scopeToAccounts(animes, view.accounts), view)
	sortAnimes(animes, view.sortKey, view.reverse)
	return printValue(animes, "No animes found")
}

func handleAnimesSearch(c *cli.Context) error {
	if err := requireArgs(c, 1, "animes search <query>"); err != nil {
		return err
	}
	results, err := getClient().SearchAniList(c.Args().Get(0), c.Bool("unreleased"))
	if err != nil {
		return mapClientError(err)
	}
	return printValue(results, "No results")
}

func handleAnimesCandidates(c *cli.Context) error {
	if err := requireArgs(c, 1, "animes candidates <id>"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	candidates, err := getClient().GetTorrentCandidatesInQueue(id, c.String("query"), c.String("queue"))
	if err != nil {
		return mapClientError(err)
	}
	return printValue(candidates, "No candidates")
}

func handleAnimesEpisodeCandidates(c *cli.Context) error {
	if err := requireArgs(c, 2, "animes episode-candidates <id> <episode>"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	episode, err := parsePositiveInt(c.Args().Get(1), "episode")
	if err != nil {
		return err
	}
	candidates, err := getClient().GetEpisodeTorrentCandidates(id, episode, c.String("query"))
	if err != nil {
		return mapClientError(err)
	}
	return printValue(candidates, "No candidates")
}

func handleAnimesCandidate(c *cli.Context) error {
	if err := requireArgs(c, 2, "animes candidate <id> <nyaa_id>"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	nyaaID, err := parsePositiveInt(c.Args().Get(1), "nyaa_id")
	if err != nil {
		return err
	}
	detail, err := getClient().GetTorrentCandidateDetail(id, nyaaID)
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(detail)
		return nil
	}
	fmt.Println(detail.NyaaURL)
	if err := writeSliceTable(detail.Files, "No files listed"); err != nil {
		return err
	}
	if m := detail.Mapping; m != nil && m.Megapack {
		printMegapackMapping(*m)
	}
	return nil
}

// printMegapackMapping is the picker's mapping table: which franchise entry each video of a
// megapack belongs to, and the videos no entry takes.
func printMegapackMapping(m api.MegapackMapping) {
	fmt.Printf("\nMegapack: %d entries, %s of %s placed\n", len(m.Entries), gibString(m.MappedBytes), gibString(m.TotalBytes))
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"Entry", "Format", "Year", "Episodes", "Files", "Size"})
	for _, e := range m.Entries {
		title := e.Title
		if e.Seed {
			title += " (this anime)"
		}
		episodes := episodeSpan(e.Episodes)
		if e.EntryEpisodes > 0 {
			episodes += fmt.Sprintf(" of %d", e.EntryEpisodes)
		}
		year := ""
		if e.Year > 0 {
			year = strconv.Itoa(e.Year)
		}
		t.AppendRow(table.Row{title, e.Format, year, episodes, len(e.Files), gibString(e.Bytes)})
	}
	t.Render()
	if len(m.Unmapped) == 0 {
		return
	}
	fmt.Printf("\nNot placed (%d):\n", len(m.Unmapped))
	u := table.NewWriter()
	u.SetOutputMirror(os.Stdout)
	u.AppendHeader(table.Row{"File", "Why"})
	for _, f := range m.Unmapped {
		u.AppendRow(table.Row{f.Name, unmappedReason(f.Reason)})
	}
	u.Render()
}

// unmappedReason words daemon.MapMegapack's reason codes.
func unmappedReason(code string) string {
	switch code {
	case daemon.UnmappedCreditless:
		return "creditless opening/ending, menu or promo"
	case daemon.UnmappedNoEntry:
		return "no entry of the franchise matches its name"
	case daemon.UnmappedPastSeries:
		return "past the last episode AniList lists"
	case daemon.UnmappedNoNumber:
		return "no episode number"
	}
	return code
}

func gibString(b int64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/float64(1<<30))
}

func handleAnimesAdd(c *cli.Context) error {
	if err := requireArgs(c, 1, "animes add <media_id>"); err != nil {
		return err
	}
	mediaID, err := parsePositiveInt(c.Args().Get(0), "media_id")
	if err != nil {
		return err
	}
	req := api.StandaloneAnimeRequest{MediaID: mediaID}
	if magnet := c.String("magnet"); magnet != "" {
		req.Magnet = magnet
		if cand := candidateForMagnet(mediaID, c.String("query"), magnet); cand != nil {
			req.Name, req.NyaaID, req.SizeBytes = cand.Name, cand.NyaaID, cand.SizeBytes
		}
	}
	if skip := c.StringSlice("skip-file"); len(skip) > 0 {
		if req.Magnet == "" {
			return fmt.Errorf("--skip-file needs --magnet: a search picks its own torrents")
		}
		req.SkipFiles = skip
	}
	req.Unblacklist = c.Bool("unblacklist")
	req.Queue = c.String("queue")
	added, err := getClient().AddStandaloneAnime(req)
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Code == "BLACKLISTED" {
		return &cliError{exitCode: 1, code: apiErr.Code, message: "This anime is on the blacklist: pass --unblacklist to take it off and add it"}
	}
	if err != nil {
		return mapClientError(err)
	}
	return printValue(added, "")
}

// candidateForMagnet finds the picker row a magnet came from, so `add --magnet` sends the same
// name / nyaa_id / size_bytes the web picker sends. Without them the daemon cannot read the episode
// range of a pack whose name carries none (decisions/084) and records no episode for it: the
// torrent downloads but never reaches the library. Best effort — a magnet from elsewhere, or a
// failed lookup, still adds with the magnet alone.
func candidateForMagnet(mediaID int, query, magnet string) *api.TorrentCandidate {
	hash, err := torrents.InfoHash(magnet)
	if err != nil {
		return nil
	}
	resp, err := getClient().GetTorrentCandidates(mediaID, query)
	if err != nil || resp == nil {
		return nil
	}
	for i := range resp.Items {
		if h, err := torrents.InfoHash(resp.Items[i].Magnet); err == nil && h == hash {
			return &resp.Items[i]
		}
	}
	return nil
}

func handleAnimesRemove(c *cli.Context) error {
	if err := requireYes(c, "--delete-episodes", "--blacklist"); err != nil {
		return err
	}
	if err := requireArgs(c, 1, "animes remove <id> --yes"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	resp, err := getClient().DeleteAnime(id, c.Bool("delete-episodes"), c.Bool("blacklist"))
	if err != nil {
		return mapClientError(err)
	}
	msg := animeRemovedMessage(resp)
	return printMessage(msg, msg)
}

// animeRemovedMessage says in one line what `animes remove` did.
func animeRemovedMessage(r *api.AnimeDeleteResponse) string {
	var parts []string
	if r.Removed {
		parts = append(parts, "Anime removed")
	}
	if r.DeletedEpisodes > 0 {
		parts = append(parts, fmt.Sprintf("%d episode(s) deleted", r.DeletedEpisodes))
	}
	if r.Blacklisted {
		parts = append(parts, "blacklisted")
	}
	if len(parts) == 0 {
		return "Nothing to do"
	}
	return strings.Join(parts, ", ")
}

func handleAnimesSettings(c *cli.Context) error {
	switch {
	case c.NArg() == 1:
		id, err := parsePositiveInt(c.Args().Get(0), "id")
		if err != nil {
			return err
		}
		settings, err := getClient().GetAnimeSettings(id)
		if err != nil {
			return mapClientError(err)
		}
		return printValue(settings, "")
	case c.NArg() == 3 && c.Args().Get(1) == "progress":
		id, err := parsePositiveInt(c.Args().Get(0), "id")
		if err != nil {
			return err
		}
		progress, err := parseNonNegativeInt(c.Args().Get(2), "progress")
		if err != nil {
			return err
		}
		if err := mapClientError(getClient().UpdateAnimeSettings(id, progress)); err != nil {
			return err
		}
		return printMessage(fmt.Sprintf("Progress set to %d", progress), "Progress updated")
	case c.NArg() >= 3 && c.NArg() <= 4 && c.Args().Get(1) == "shoko":
		id, err := parsePositiveInt(c.Args().Get(0), "id")
		if err != nil {
			return err
		}
		var match api.ShokoMatchRequest
		switch arg := c.Args().Get(2); {
		case arg == "none" && c.NArg() == 3:
			match.None = true
		case arg == "auto" && c.NArg() == 3:
			match.Auto = true
		default:
			if match.SeriesID, err = parsePositiveInt(arg, "series-id"); err != nil {
				return err
			}
			if c.NArg() == 4 {
				if match.Offset, err = parseNonNegativeInt(c.Args().Get(3), "offset"); err != nil {
					return err
				}
			}
		}
		if err := mapClientError(getClient().UpdateAnimeShoko(id, match)); err != nil {
			return err
		}
		return printMessage("Shoko match updated; the next check pass applies it", "Shoko match updated")
	case c.NArg() == 3 && c.Args().Get(1) == "shoko-packs":
		id, err := parsePositiveInt(c.Args().Get(0), "id")
		if err != nil {
			return err
		}
		value := c.Args().Get(2)
		if value != files.ShokoPackDownload && value != files.ShokoPackRemove && value != api.ShokoPackDefault {
			return fmt.Errorf("shoko-packs takes download, remove or default, not %q", value)
		}
		if err := mapClientError(getClient().UpdateAnimeShokoPacks(id, value)); err != nil {
			return err
		}
		return printMessage("Pack setting updated; the next check applies it", "Pack setting updated")
	case c.NArg() == 3 && c.Args().Get(1) == "root":
		id, err := parsePositiveInt(c.Args().Get(0), "id")
		if err != nil {
			return err
		}
		root := c.Args().Get(2)
		if root == "none" {
			root = ""
		}
		if err := mapClientError(getClient().UpdateAnimeRoot(id, root)); err != nil {
			return err
		}
		if root == "" {
			return printMessage("Download folder override removed", "Download folder override removed")
		}
		return printMessage("New torrents of this anime go to "+root+" first", "Download folder set")
	default:
		return fmt.Errorf("usage: animes settings <id> [progress <n> | shoko <series-id> [offset] | shoko none | shoko auto | shoko-packs download|remove|default | root <name> | root none]")
	}
}

func handleAnimesQueue(c *cli.Context) error {
	if err := requireArgs(c, 2, "animes queue <id> <queue-id>"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	queue := c.Args().Get(1)
	if err := mapClientError(getClient().MoveAnimeToQueue(id, queue)); err != nil {
		return err
	}
	return printMessage("Anime moved to queue "+queue, "Anime moved")
}

func handleAnimesEpisodes(c *cli.Context) error {
	if err := requireArgs(c, 1, "animes episodes <id>"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	detail, err := getClient().GetAnimeEpisodes(id)
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(detail)
		return nil
	}
	if err := writeSliceTable(detail.Episodes, "No episodes found"); err != nil {
		return err
	}
	// The anime page's "on lists" panel: every configured account that has it, in any status.
	if len(detail.Accounts) > 0 {
		fmt.Println("\nOn lists:")
		return writeSliceTable(detail.Accounts, "")
	}
	return nil
}

func handleAnimesReleases(c *cli.Context) error {
	if err := requireArgs(c, 1, "animes releases <id>"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	cards, err := getClient().AnimeReleases(id)
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(cards)
		return nil
	}
	type row struct {
		Name     string `json:"name"`
		Episodes string `json:"episodes"`
		State    string `json:"state"`
		Origin   string `json:"origin"`
		Tags     string `json:"tags"`
	}
	rows := make([]row, len(cards))
	for i, c := range cards {
		origin := c.Origin
		if !c.Recorded {
			origin = "before records"
		}
		rows[i] = row{Name: c.Name, Episodes: episodeSpan(c.Episodes), State: c.State, Origin: origin, Tags: releaseTags(c.Release)}
	}
	return writeSliceTable(rows, "No torrents for this anime")
}

// episodeSpan writes episodes as "5", "1-12", or "1-3, 5" when there are gaps.
func episodeSpan(eps []int) string {
	var parts []string
	for i := 0; i < len(eps); {
		j := i
		for j+1 < len(eps) && eps[j+1] == eps[j]+1 {
			j++
		}
		if i == j {
			parts = append(parts, strconv.Itoa(eps[i]))
		} else {
			parts = append(parts, strconv.Itoa(eps[i])+"-"+strconv.Itoa(eps[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// releaseTags is a release's tags on one line, an assumed value marked with "?".
func releaseTags(r nyaa.Release) string {
	assumed := func(field, v string) string {
		if slices.Contains(r.Assumed, field) {
			return v + "?"
		}
		return v
	}
	var tags []string
	for _, v := range []string{r.Resolution, assumed("source", r.Source), r.Service, r.VideoCodec, r.AudioCodec, r.AudioChannels} {
		if v != "" {
			tags = append(tags, v)
		}
	}
	if r.BitDepth > 0 {
		tags = append(tags, strconv.Itoa(r.BitDepth)+"bit")
	}
	if len(r.AudioLanguages) > 0 {
		tags = append(tags, "audio "+assumed("audio_languages", strings.Join(r.AudioLanguages, "+")))
	}
	if r.Hardsub {
		tags = append(tags, "hardsub")
	} else if len(r.SubtitleLanguages) > 0 {
		tags = append(tags, "subs "+assumed("subtitle_languages", strings.Join(r.SubtitleLanguages, "+")))
	}
	if r.Version > 1 {
		tags = append(tags, "v"+strconv.Itoa(r.Version))
	}
	return strings.Join(tags, " ")
}

func handleAnimesDebug(c *cli.Context) error {
	if err := requireArgs(c, 1, "animes debug <id>"); err != nil {
		return err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	summary, err := getClient().RunAnimeDebug(id)
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(summary)
		return nil
	}
	fmt.Printf("%s (%d)\n", summary.AnimeName, summary.AnimeID)
	return writeSliceTable(summary.Episodes, "No episodes selected")
}

func handleAnimesDownload(c *cli.Context) error {
	id, ep, err := animeEpisodeArgs(c, "animes download <id> <episode>")
	if err != nil {
		return err
	}
	if err := mapClientError(getClient().DownloadEpisode(id, ep)); err != nil {
		return err
	}
	return printMessage("Episode download started", "Episode download started")
}

func handleAnimesRedownload(c *cli.Context) error {
	if err := requireYes(c); err != nil {
		return err
	}
	id, episodes, err := animeEpisodesArgs(c, "animes redownload <id> <episode> [<episode> ...] --yes")
	if err != nil {
		return err
	}
	client := getClient()
	if len(episodes) == 1 {
		if err := mapClientError(client.RedownloadEpisode(id, episodes[0])); err != nil {
			return err
		}
		return printMessage("Episode redownload started", "Episode redownload started")
	}

	result, err := client.BatchRedownloadEpisodes(id, episodes)
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(result)
	} else {
		fmt.Printf("Redownload started: %v\n", result.Redownloaded)
		for _, f := range result.Failed {
			fmt.Printf("Failed: episode %d: %s\n", f.Episode, f.Error)
		}
	}
	if len(result.Failed) > 0 {
		return fmt.Errorf("%d of %d episodes failed to redownload", len(result.Failed), len(result.Failed)+len(result.Redownloaded))
	}
	return nil
}

func handleAnimesDelete(c *cli.Context) error {
	if err := requireYes(c); err != nil {
		return err
	}
	id, episodes, err := animeEpisodesArgs(c, "animes delete <id> <episode> [<episode> ...] --yes")
	if err != nil {
		return err
	}
	client := getClient()
	if len(episodes) == 1 {
		if err := mapClientError(client.DeleteEpisode(id, episodes[0])); err != nil {
			return err
		}
		return printMessage("Episode deleted", "Episode deleted")
	}
	if err := mapClientError(client.BatchDeleteEpisodes(id, episodes)); err != nil {
		return err
	}
	return printMessage(fmt.Sprintf("%d episodes deleted", len(episodes)), "Episodes deleted")
}

func handleAnimesRelease(c *cli.Context) error {
	id, ep, err := animeEpisodeArgs(c, "animes release <id> <episode>")
	if err != nil {
		return err
	}
	if err := mapClientError(getClient().ReleaseEpisode(id, ep)); err != nil {
		return err
	}
	return printMessage("Episode released", "Episode released")
}

func handleAnimesReplace(c *cli.Context) error {
	if err := requireYes(c); err != nil {
		return err
	}
	if err := requireArgs(c, 1, "animes replace <id> --magnet URI --yes"); err != nil {
		return err
	}
	magnet := c.String("magnet")
	if magnet == "" {
		return fmt.Errorf("usage: animes replace <id> --magnet URI --yes")
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return err
	}
	req := api.ReplaceAnimeRequest{Magnet: magnet, SkipFiles: c.StringSlice("skip-file")}
	if len(req.SkipFiles) > 0 {
		// The picker row, as `add --magnet` sends it: the daemon reads the file list by its Nyaa id
		// to leave the unticked episodes unrecorded (decisions/165).
		if cand := candidateForMagnet(id, "", magnet); cand != nil {
			req.Name, req.NyaaID, req.SizeBytes = cand.Name, cand.NyaaID, cand.SizeBytes
		}
	}
	if err := mapClientError(getClient().ReplaceAnime(id, req)); err != nil {
		return err
	}
	return printMessage("Anime replacement started", "Anime replacement started")
}

func handleAnimesReplaceEpisode(c *cli.Context) error {
	if err := requireYes(c); err != nil {
		return err
	}
	id, ep, err := animeEpisodeArgs(c, "animes replace-episode <id> <episode> --magnet URI --yes")
	if err != nil {
		return err
	}
	magnet := c.String("magnet")
	if magnet == "" {
		return fmt.Errorf("usage: animes replace-episode <id> <episode> --magnet URI --yes")
	}
	if err := mapClientError(getClient().ReplaceEpisode(id, ep, magnet)); err != nil {
		return err
	}
	return printMessage("Episode replacement started", "Episode replacement started")
}

func animeEpisodeArgs(c *cli.Context, usage string) (int, int, error) {
	if err := requireArgs(c, 2, usage); err != nil {
		return 0, 0, err
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return 0, 0, err
	}
	ep, err := parsePositiveInt(c.Args().Get(1), "episode")
	if err != nil {
		return 0, 0, err
	}
	return id, ep, nil
}

// animeEpisodesArgs is animeEpisodeArgs for commands that take one or more episodes.
func animeEpisodesArgs(c *cli.Context, usage string) (int, []int, error) {
	if c.NArg() < 2 {
		return 0, nil, fmt.Errorf("usage: %s", usage)
	}
	id, err := parsePositiveInt(c.Args().Get(0), "id")
	if err != nil {
		return 0, nil, err
	}
	episodes := make([]int, 0, c.NArg()-1)
	for _, arg := range c.Args().Slice()[1:] {
		ep, err := parsePositiveInt(arg, "episode")
		if err != nil {
			return 0, nil, err
		}
		episodes = append(episodes, ep)
	}
	return id, episodes, nil
}

func parseNonNegativeInt(s, name string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s %q", name, s)
	}
	return n, nil
}
