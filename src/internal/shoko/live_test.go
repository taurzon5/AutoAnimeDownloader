package shoko

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestShokoLive reads a real Shoko Server set up by scripts/shoko-live.sh (docs/agents/testing.md,
// "Shoko Live Test"): Frieren with dummy files for episodes 1 and 2. It checks what the fake can only
// claim: the sign-in, the series' MAL ids, the owned episodes, the newest-first file order, the sync
// and the match. Only runs with AAD_LIVE_SHOKO=1, so `go test ./...` doesn't need Docker.
func TestShokoLive(t *testing.T) {
	if os.Getenv("AAD_LIVE_SHOKO") != "1" {
		t.Skip("set AAD_LIVE_SHOKO=1 and the AAD_SHOKO_* variables to run against a real Shoko")
	}
	env := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			t.Fatalf("%s is not set", key)
		}
		return v
	}
	url := env("AAD_SHOKO_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	key, err := Login(ctx, url, env("AAD_SHOKO_USER"), env("AAD_SHOKO_PASSWORD"))
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	c, err := NewClient(url, key)
	if err != nil {
		t.Fatal(err)
	}
	all, err := c.AllSeries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var frieren Series
	for _, s := range all {
		if s.AniDB == 17617 {
			frieren = s
		}
	}
	t.Logf("series: %+v", frieren)
	if frieren.ID == 0 || frieren.Episodes != 28 || len(frieren.MAL) < 2 || frieren.MAL[0] != 52991 {
		t.Fatalf("Frieren = %+v, want 28 episodes and MAL 52991 first, with its shorts", frieren)
	}

	owned, err := c.OwnedEpisodes(ctx, frieren.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 2 || owned[0].Number != 1 || owned[1].Number != 2 || owned[0].Files[0].Path == "" || !owned[0].Files[0].Accessible {
		t.Fatalf("owned episodes = %+v, want 1 and 2 with their files", owned)
	}

	files, err := c.FilesImportedSince(ctx, time.Time{})
	if err != nil || len(files) != 2 {
		t.Fatalf("FilesImportedSince = %+v, %v; want both files", files, err)
	}
	if !files[0].Imported.After(files[1].Imported) || len(files[0].Series) != 1 || files[0].Series[0] != frieren.ID {
		t.Fatalf("files = %+v, want newest first, each naming its series", files)
	}
	newest, id, err := c.NewestImport(ctx)
	if err != nil || !newest.Equal(files[0].Imported) || id != files[0].ID {
		t.Fatalf("NewestImport = %v, %d, %v; want the first of the newest-first list", newest, id, err)
	}

	store := NewStore(t.TempDir())
	tracked := map[int]bool{52991: true}
	for _, when := range []time.Time{time.Now(), time.Now().Add(time.Hour)} {
		if err := store.Sync(ctx, c, url, tracked, false, when); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}
	entry := Entry{MalID: 52991, Episodes: 28}
	m := MatchEntry(entry, store.SeriesList(), nil)
	if m.Status != Matched || m.SeriesID != frieren.ID || m.Offset != 0 {
		t.Fatalf("match = %+v, want Frieren at offset 0", m)
	}
	if got := OwnedOf(m, entry, store.OwnedEpisodes(frieren.ID)); len(got) != 2 {
		t.Fatalf("owned of the entry = %+v, want episodes 1 and 2", got)
	}
	if shorts := MatchEntry(Entry{MalID: 56885, Episodes: 10}, store.SeriesList(), nil); shorts.Status != Partial {
		t.Fatalf("the shorts' entry = %+v, want Partial", shorts)
	}
}
