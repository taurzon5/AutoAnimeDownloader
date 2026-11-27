package shoko

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// signalRSeparator ends every message of SignalR's JSON protocol.
const signalRSeparator = "\x1e"

// watchPing is how often the client tells the hub it is alive; SignalR drops a client silent for
// 30 s by default.
const watchPing = 15 * time.Second

// Change is a file event of the series it names: a file linked to one of their episodes
// (FileMatched) or deleted (FileDeleted). Measured on v5.3.3: those two carry the series in their
// cross-references; FileDetected and FileHashed don't yet, and unlinking a file from an episode
// sends nothing at all (a reconnection's catch-up or a full resync sees it, decisions/148).
//
// Shoko 6 names the series another way (measured on v6.0.0): a link or an unlink is
// release:saved / release:removed with the file's id and its cross-references as AniDB URIs
// ("anidb://series/20035"), so Series is empty and AniDB holds the AniDB anime ids, Files the file
// ids; the daemon turns them into Shoko series (SeriesForChange). Unlinking now sends an event.
type Change struct {
	Event  string
	Series []int
	// AniDB are the AniDB anime ids a Shoko 6 event names.
	AniDB []int
	// Files are the Shoko file ids a Shoko 6 event names.
	Files []int
}

// Watch connects to Shoko's SignalR hub (/signalr/aggregate: the "shoko" feed on Shoko 5, "release"
// and "file" on Shoko 6) and calls onChange for every file event that names a series, an AniDB
// anime or a file, until ctx ends or the connection fails, which it returns. onConnected runs once the hub answered the handshake ({}), the one signal every feed
// gives.
func (c *Client) Watch(ctx context.Context, onConnected func(), onChange func(Change)) error {
	// v5.3.3 sends the file events on the "shoko" feed (ShokoEvent:File…; its "file" feed sends none
	// of them). v6.0.0 has no "shoko" feed: links and unlinks are on "release", deletions on "file".
	// A hub ignores a feed it doesn't know, so one list serves both.
	feeds := url.Values{"feeds": {"shoko,file,release"}}
	negotiate := c.base.String() + "/signalr/aggregate/negotiate?negotiateVersion=1&" + feeds.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, negotiate, nil)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("shoko did not answer: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("shoko's event hub answered HTTP %d", resp.StatusCode)
	}
	var neg struct {
		ConnectionToken string `json:"connectionToken"`
	}
	if err := json.Unmarshal(body, &neg); err != nil || neg.ConnectionToken == "" {
		return errors.New("shoko's event hub gave no connection token")
	}

	ws := strings.Replace(c.base.String(), "http", "ws", 1) + "/signalr/aggregate?" + feeds.Encode() + "&id=" + url.QueryEscape(neg.ConnectionToken)
	conn, _, err := websocket.Dial(ctx, ws, &websocket.DialOptions{HTTPHeader: http.Header{"apikey": {c.apiKey}}})
	if err != nil {
		return fmt.Errorf("could not open shoko's event hub: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"protocol":"json","version":1}`+signalRSeparator)); err != nil {
		return err
	}

	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		t := time.NewTicker(watchPing)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				_ = conn.Write(pingCtx, websocket.MessageText, []byte(`{"type":6}`+signalRSeparator))
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("shoko's event hub closed: %w", err)
		}
		for _, raw := range strings.Split(string(data), signalRSeparator) {
			if raw == "" {
				continue
			}
			if ch, connected, closed := parseHubMessage(raw); closed != nil {
				return closed
			} else if connected {
				if onConnected != nil {
					onConnected()
				}
			} else if len(ch.Series)+len(ch.AniDB)+len(ch.Files) > 0 && onChange != nil {
				onChange(ch)
			}
		}
	}
}

// parseHubMessage reads one SignalR message: the handshake answer ({}, connected; with an error, a
// refusal), a file event (Shoko 5's naming series, Shoko 6's naming AniDB anime and files), or a
// close (type 7) as an error. Pings and other events
// are nothing.
func parseHubMessage(raw string) (Change, bool, error) {
	var msg struct {
		Type      int               `json:"type"`
		Target    string            `json:"target"`
		Arguments []json.RawMessage `json:"arguments"`
		Error     string            `json:"error"`
	}
	if json.Unmarshal([]byte(raw), &msg) != nil {
		return Change{}, false, nil
	}
	switch {
	case msg.Type == 0 && msg.Error != "":
		return Change{}, false, fmt.Errorf("shoko's event hub refused the handshake: %s", msg.Error)
	case msg.Type == 0:
		return Change{}, true, nil
	case msg.Type == 7:
		if msg.Error == "" {
			msg.Error = "closed by the server"
		}
		return Change{}, false, fmt.Errorf("shoko's event hub: %s", msg.Error)
	case msg.Type != 1:
		return Change{}, false, nil
	}
	if event, ok := shoko6Events[msg.Target]; ok {
		return parseShoko6Change(event, msg.Arguments), false, nil
	}
	if msg.Target != "ShokoEvent:FileMatched" && msg.Target != "ShokoEvent:FileDeleted" {
		return Change{}, false, nil
	}
	ch := Change{Event: strings.TrimPrefix(msg.Target, "ShokoEvent:")}
	seen := map[int]bool{}
	for _, arg := range msg.Arguments {
		var ev struct {
			CrossReferences []struct {
				SeriesID int `json:"SeriesID"`
			} `json:"CrossReferences"`
		}
		if json.Unmarshal(arg, &ev) != nil {
			continue
		}
		for _, x := range ev.CrossReferences {
			if x.SeriesID > 0 && !seen[x.SeriesID] {
				seen[x.SeriesID] = true
				ch.Series = append(ch.Series, x.SeriesID)
			}
		}
	}
	return ch, false, nil
}

// shoko6Events are Shoko 6's file events and the v5 name each one stands for (Shoko's own web UI
// maps them the same way).
var shoko6Events = map[string]string{
	"release:saved":   "FileMatched",
	"release:removed": "FileMatched",
	"file:deleted":    "FileDeleted",
}

// parseShoko6Change reads a Shoko 6 file event: {"FileID": 6, "Release": {"CrossReferences":
// [{"IDs": ["anidb://episode/316938", "anidb://series/20035"]}]}} (release:saved, measured on
// v6.0.0). A cross-reference may sit at the top level too; both are read.
func parseShoko6Change(event string, args []json.RawMessage) Change {
	ch := Change{Event: event}
	seenFile, seenAniDB := map[int]bool{}, map[int]bool{}
	type xref struct {
		IDs []string `json:"IDs"`
	}
	for _, arg := range args {
		var ev struct {
			FileID          int    `json:"FileID"`
			CrossReferences []xref `json:"CrossReferences"`
			Release         struct {
				CrossReferences []xref `json:"CrossReferences"`
			} `json:"Release"`
		}
		if json.Unmarshal(arg, &ev) != nil {
			continue
		}
		if ev.FileID > 0 && !seenFile[ev.FileID] {
			seenFile[ev.FileID] = true
			ch.Files = append(ch.Files, ev.FileID)
		}
		for _, x := range append(ev.CrossReferences, ev.Release.CrossReferences...) {
			for _, id := range x.IDs {
				rest, ok := strings.CutPrefix(id, "anidb://series/")
				if !ok {
					continue
				}
				if n, err := strconv.Atoi(rest); err == nil && n > 0 && !seenAniDB[n] {
					seenAniDB[n] = true
					ch.AniDB = append(ch.AniDB, n)
				}
			}
		}
	}
	return ch
}
