package main

import (
	"fmt"
	"strconv"

	"AutoAnimeDownloader/src/internal/api"

	"github.com/urfave/cli/v2"
)

func torrentsCommand() *cli.Command {
	return &cli.Command{
		Name:  "torrents",
		Usage: "List and manage torrents",
		Action: func(c *cli.Context) error {
			return handleTorrents()
		},
		Subcommands: []*cli.Command{
			{
				Name:      "files",
				Usage:     "List files inside a torrent, numbered for skip and want",
				ArgsUsage: "<hash>",
				Action:    handleTorrentFiles,
			},
			{
				Name:      "skip",
				Usage:     "Stop downloading files of a torrent (qBittorrent only); a file already on disk is deleted",
				ArgsUsage: "<hash> <file…>",
				Description: "Files are the numbers `torrents files` prints, or their paths. qBittorrent stops\n" +
					"downloading them and keeps seeding the rest. A file already downloaded is deleted,\n" +
					"unless it is finished in one of Shoko's import folders. An episode left with no file\n" +
					"loses its record: the app may then download it from another torrent, unless --block.",
				Flags: []cli.Flag{
					yesFlag(),
					&cli.BoolFlag{Name: "block", Usage: "Block the episodes left with no file from automatic download"},
				},
				Action: handleTorrentSkip,
			},
			{
				Name:      "want",
				Usage:     "Download skipped files of a torrent again (qBittorrent only)",
				ArgsUsage: "<hash> <file…>",
				Description: "Files are the numbers `torrents files` prints, or their paths. Their episodes get\n" +
					"their records back and are unblocked.",
				Action: handleTorrentWant,
			},
			{
				Name:      "pause",
				Usage:     "Pause a torrent",
				ArgsUsage: "<hash>",
				Action:    handleTorrentPause,
			},
			{
				Name:      "resume",
				Usage:     "Resume a torrent",
				ArgsUsage: "<hash>",
				Action:    handleTorrentResume,
			},
			{
				Name:      "announce",
				Usage:     "Re-announce a torrent to trackers",
				ArgsUsage: "<hash>",
				Action:    handleTorrentAnnounce,
			},
			{
				Name:      "prioritize",
				Usage:     "Move torrent(s) to the front of the download queue",
				ArgsUsage: "<hash> [hash…]",
				Action:    handleTorrentPrioritize,
			},
			{
				Name:      "redownload-missing",
				Usage:     "Download again the missing files of finished direct-mode torrents (files deleted outside the app); all of them, or only the hashes given",
				ArgsUsage: "[hash…]",
				Action:    handleTorrentsRedownloadMissing,
			},
			{
				Name:   "test",
				Usage:  "Test the connection to qBittorrent with the saved settings: sign-in, versions, and whether it sees the library folder",
				Action: handleTorrentClientTest,
			},
			{
				Name:      "delete",
				Usage:     "Delete a torrent",
				ArgsUsage: "<hash>",
				Flags: []cli.Flag{
					yesFlag(),
					&cli.BoolFlag{Name: "keep-data", Usage: "Keep downloaded files"},
					&cli.BoolFlag{Name: "block", Usage: "Block the episode from automatic redownload"},
				},
				Action: handleTorrentDelete,
			},
		},
	}
}

func handleTorrents() error {
	list, err := getClient().GetTorrents()
	if err != nil {
		return mapClientError(err)
	}
	return printValue(list, "No torrents found")
}

func handleTorrentsRedownloadMissing(c *cli.Context) error {
	result, err := getClient().RedownloadMissingFiles(c.Args().Slice())
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(result)
		return nil
	}
	fmt.Printf("Redownloading %d torrent(s)", result.Redownloaded)
	if result.SkippedNoSpace > 0 {
		fmt.Printf("; %d skipped for lack of disk space", result.SkippedNoSpace)
	}
	fmt.Println()
	return nil
}

func handleTorrentClientTest(c *cli.Context) error {
	result, err := getClient().TestTorrentClient()
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(result)
		return nil
	}
	fmt.Printf("Connected to qBittorrent %s (Web API %s)\n", result.AppVersion, result.WebAPIVersion)
	switch result.SignIn {
	case "api_key":
		fmt.Println("Signed in with the API key")
	case "password":
		fmt.Println("Signed in with the username and password")
	}
	switch {
	case result.Problem != "":
		fmt.Printf("Problem: %s\n", result.Problem)
	case result.LibraryVisible:
		fmt.Printf("qBittorrent sees the library at %s\n", result.LibraryPath)
		if result.IncompleteVisible {
			fmt.Printf("qBittorrent sees the incomplete folder at %s\n", result.IncompletePath)
		}
		for _, f := range result.Folders {
			if f.Visible {
				fmt.Printf("qBittorrent sees the download folder %q at %s\n", f.Name, f.Path)
			}
		}
	}
	switch q := result.QueueingEnabled; {
	case q == nil:
	case !*q:
		fmt.Println("Its download queue is off: every download runs at once")
	case result.MaxActiveDownloads == nil || *result.MaxActiveDownloads < 0:
		fmt.Println("Its download queue has no limit")
	default:
		fmt.Printf("Its download queue runs %d at a time\n", *result.MaxActiveDownloads)
	}
	if d := result.DiskIOType; d != nil && (*d == 0 || *d == 1) {
		fmt.Println("It uses memory-mapped disk I/O: for a library on a USB or network disk, Simple pread/pwrite (its Advanced settings, then a restart) is safer")
	}
	return nil
}

func handleTorrentFiles(c *cli.Context) error {
	hash, err := torrentHashArg(c, "torrents files <hash>")
	if err != nil {
		return err
	}
	files, err := getClient().GetTorrentFiles(hash)
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		return printValue(files, "No files found")
	}
	numbered := make([]numberedTorrentFile, len(files))
	for i, f := range files {
		numbered[i] = numberedTorrentFile{Number: i + 1, TorrentFileResponse: f}
	}
	return printValue(numbered, "No files found")
}

// numberedTorrentFile is a row of `torrents files`: the number `torrents skip` and `want` take.
type numberedTorrentFile struct {
	Number int `json:"number"`
	api.TorrentFileResponse
}

func handleTorrentSkip(c *cli.Context) error {
	if err := requireYes(c, "--block"); err != nil {
		return err
	}
	hash, paths, err := torrentFileArgs(c, "torrents skip <hash> <file…> --yes")
	if err != nil {
		return err
	}
	result, err := getClient().ChooseTorrentFiles(hash, api.TorrentFileChoiceRequest{Skip: paths, Block: c.Bool("block")})
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(result)
		return nil
	}
	fmt.Printf("Skipped %d file(s), deleted %d", result.Skipped, result.Deleted)
	if result.Kept > 0 {
		fmt.Printf(", kept %d in Shoko's folders", result.Kept)
	}
	fmt.Printf("; %d episode record(s) removed\n", result.RecordsRemoved)
	return nil
}

func handleTorrentWant(c *cli.Context) error {
	hash, paths, err := torrentFileArgs(c, "torrents want <hash> <file…>")
	if err != nil {
		return err
	}
	result, err := getClient().ChooseTorrentFiles(hash, api.TorrentFileChoiceRequest{Want: paths})
	if err != nil {
		return mapClientError(err)
	}
	if outputJSON {
		outputJSONResponse(result)
		return nil
	}
	fmt.Printf("Downloading %d file(s) again; %d episode record(s) back\n", result.Wanted, result.RecordsAdded)
	return nil
}

// torrentFileArgs reads <hash> <file…>: a file is a number of `torrents files` (1-based) or a path
// it prints.
func torrentFileArgs(c *cli.Context, usage string) (string, []string, error) {
	if c.NArg() < 2 {
		return "", nil, fmt.Errorf("usage: %s", usage)
	}
	hash := c.Args().Get(0)
	args := c.Args().Slice()[1:]
	var list []api.TorrentFileResponse
	paths := make([]string, 0, len(args))
	for _, a := range args {
		n, err := strconv.Atoi(a)
		if err != nil {
			paths = append(paths, a)
			continue
		}
		if list == nil {
			if list, err = getClient().GetTorrentFiles(hash); err != nil {
				return "", nil, mapClientError(err)
			}
		}
		if n < 1 || n > len(list) {
			return "", nil, fmt.Errorf("file %d: the torrent has %d files (see `torrents files %s`)", n, len(list), hash)
		}
		paths = append(paths, list[n-1].Path)
	}
	return hash, paths, nil
}

func handleTorrentPause(c *cli.Context) error {
	hash, err := torrentHashArg(c, "torrents pause <hash>")
	if err != nil {
		return err
	}
	if err := mapClientError(getClient().PauseTorrent(hash)); err != nil {
		return err
	}
	return printMessage("Torrent paused", "Torrent paused")
}

func handleTorrentResume(c *cli.Context) error {
	hash, err := torrentHashArg(c, "torrents resume <hash>")
	if err != nil {
		return err
	}
	if err := mapClientError(getClient().ResumeTorrent(hash)); err != nil {
		return err
	}
	return printMessage("Torrent resumed", "Torrent resumed")
}

func handleTorrentAnnounce(c *cli.Context) error {
	hash, err := torrentHashArg(c, "torrents announce <hash>")
	if err != nil {
		return err
	}
	if err := mapClientError(getClient().AnnounceTorrent(hash)); err != nil {
		return err
	}
	return printMessage("Announce sent", "Announce sent")
}

func handleTorrentPrioritize(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("usage: torrents prioritize <hash> [hash…]")
	}
	hashes := c.Args().Slice()
	var err error
	if len(hashes) == 1 {
		err = getClient().PrioritizeTorrent(hashes[0])
	} else {
		err = getClient().PrioritizeTorrents(hashes)
	}
	if err != nil {
		return mapClientError(err)
	}
	return printMessage("Torrent(s) prioritized", "Torrents prioritized")
}

func handleTorrentDelete(c *cli.Context) error {
	if err := requireYes(c, "--keep-data", "--block"); err != nil {
		return err
	}
	hash, err := torrentHashArg(c, "torrents delete <hash> --yes")
	if err != nil {
		return err
	}
	if err := mapClientError(getClient().DeleteTorrent(hash, c.Bool("keep-data"), c.Bool("block"))); err != nil {
		return err
	}
	return printMessage("Torrent deleted", "Torrent deleted")
}

func torrentHashArg(c *cli.Context, usage string) (string, error) {
	if err := requireArgs(c, 1, usage); err != nil {
		return "", err
	}
	hash := c.Args().Get(0)
	if hash == "" {
		return "", fmt.Errorf("usage: %s", usage)
	}
	return hash, nil
}
