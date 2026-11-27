package daemon

import (
	"fmt"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/nyaa"
)

const gib = int64(1) << 30

func tvEntry(id, year, episodes int, romaji string) anilist.FranchiseEntry {
	return anilist.FranchiseEntry{ID: id, Format: anilist.MediaFormatTV, Year: year, Episodes: episodes, Romaji: romaji}
}

func otherEntry(id int, format anilist.MediaFormat, year, episodes int, romaji, english string) anilist.FranchiseEntry {
	return anilist.FranchiseEntry{ID: id, Format: format, Year: year, Episodes: episodes, Romaji: romaji, English: english}
}

// chainOf gives each id its offset on one series axis, in order.
func chainOf(entries ...anilist.FranchiseEntry) map[int]anilist.Series {
	out := map[int]anilist.Series{}
	offset := 0
	for _, e := range entries {
		out[e.ID] = anilist.Series{Key: entries[0].ID, Offset: offset}
		offset += e.Episodes
	}
	return out
}

func seasonFiles(folder, show string, season, count int) []nyaa.TorrentFile {
	out := make([]nyaa.TorrentFile, 0, count)
	for ep := 1; ep <= count; ep++ {
		out = append(out, nyaa.TorrentFile{
			Name:      fmt.Sprintf("Root/%s/%s - S%02dE%02d [1080p].mkv", folder, show, season, ep),
			SizeBytes: gib / 4,
		})
	}
	return out
}

func entryByID(m MegapackMapping, id int) *MegapackEntry {
	for i := range m.Entries {
		if m.Entries[i].ID == id {
			return &m.Entries[i]
		}
	}
	return nil
}

// The measured Trix layout: five "Season N" folders, numbering restarting in each, laid end to end
// on the series axis and cut back into the five TV entries.
func TestMapMegapack_RestartingSeasonFoldersLandOnTheirEntries(t *testing.T) {
	s1 := tvEntry(1, 2019, 26, "Kimetsu no Yaiba")
	s2 := tvEntry(2, 2021, 7, "Kimetsu no Yaiba: Mugen Ressha-hen")
	s3 := tvEntry(3, 2021, 11, "Kimetsu no Yaiba: Yuukaku-hen")
	s4 := tvEntry(4, 2023, 11, "Kimetsu no Yaiba: Katanakaji no Sato-hen")
	s5 := tvEntry(5, 2024, 8, "Kimetsu no Yaiba: Hashira Geiko-hen")
	movie := otherEntry(6, anilist.MediaFormatMovie, 2020, 1, "Kimetsu no Yaiba Movie: Mugen Ressha-hen", "Demon Slayer Movie: Mugen Train")
	group := anilist.Franchise{SeedID: 3, Entries: []anilist.FranchiseEntry{s1, movie, s2, s3, s4, s5}}

	var files []nyaa.TorrentFile
	for i, n := range []int{26, 7, 11, 11, 8} {
		files = append(files, seasonFiles(fmt.Sprintf("Season %d", i+1), "Kimetsu no Yaiba", i+1, n)...)
	}
	files = append(files,
		nyaa.TorrentFile{Name: "Root/Season 1/NCOP/Kimetsu no Yaiba - NCOP01.mkv", SizeBytes: 100},
		nyaa.TorrentFile{Name: "Root/Season 1/Kimetsu no Yaiba - S01E01 [1080p].ass", SizeBytes: 10},
	)

	m := MapMegapack(files, group, chainOf(s1, s2, s3, s4, s5))

	if !m.Megapack {
		t.Fatal("five seasons should read as a megapack")
	}
	want := map[int]int{1: 26, 2: 7, 3: 11, 4: 11, 5: 8}
	for id, n := range want {
		e := entryByID(m, id)
		if e == nil || len(e.Episodes) != n || e.Episodes[0] != 1 || e.Episodes[n-1] != n {
			t.Errorf("entry %d = %+v, want episodes 1..%d", id, e, n)
		}
	}
	if s := entryByID(m, 3); s == nil || !s.Seed {
		t.Error("the seed entry should be marked")
	}
	if entryByID(m, 6) != nil {
		t.Error("the movie has no file in this pack")
	}
	if len(m.Unmapped) != 1 || m.Unmapped[0].Reason != UnmappedCreditless {
		t.Errorf("unmapped = %+v, want the NCOP only (subtitles are not videos)", m.Unmapped)
	}
	if m.TotalBytes != m.MappedBytes+100+10 {
		t.Errorf("bytes: total %d, mapped %d", m.TotalBytes, m.MappedBytes)
	}
}

// Seasons named in words ("First Season") order the same way.
func TestMapMegapack_SeasonsInWords(t *testing.T) {
	s1 := tvEntry(1, 2014, 25, "Haikyuu!!")
	s2 := tvEntry(2, 2015, 25, "Haikyuu!! Second Season")
	s3 := tvEntry(3, 2016, 10, "Haikyuu!! Karasuno Koukou vs Shiratorizawa Gakuen Koukou")
	group := anilist.Franchise{SeedID: 1, Entries: []anilist.FranchiseEntry{s1, s2, s3}}
	var files []nyaa.TorrentFile
	for i, folder := range []string{"Third Season", "First Season", "Second Season"} {
		n := []int{10, 25, 25}[i]
		for ep := 1; ep <= n; ep++ {
			files = append(files, nyaa.TorrentFile{Name: fmt.Sprintf("Haikyuu!! Complete Batch/%s/Haikyuu - %02d.mkv", folder, ep), SizeBytes: 1})
		}
	}
	m := MapMegapack(files, group, chainOf(s1, s2, s3))
	for id, n := range map[int]int{1: 25, 2: 25, 3: 10} {
		if e := entryByID(m, id); e == nil || len(e.Files) != n {
			t.Errorf("entry %d = %+v, want %d files", id, e, n)
		}
	}
	if len(m.Unmapped) != 0 {
		t.Errorf("unmapped = %+v", m.Unmapped)
	}
}

// A flat absolute range on a single TV entry is a season pack, not a megapack; episodes past the
// entries AniList lists stay unmapped.
func TestMapMegapack_FlatRangeOnOneEntry(t *testing.T) {
	bleach := tvEntry(1, 2004, 366, "Bleach")
	group := anilist.Franchise{SeedID: 1, Entries: []anilist.FranchiseEntry{bleach}}
	var files []nyaa.TorrentFile
	for ep := 1; ep <= 367; ep++ {
		files = append(files, nyaa.TorrentFile{Name: fmt.Sprintf("Bleach/[Erai-raws] Bleach - %03d [1080p].mkv", ep), SizeBytes: 1})
	}
	m := MapMegapack(files, group, chainOf(bleach))
	if m.Megapack {
		t.Error("one entry is not a megapack")
	}
	if e := entryByID(m, 1); e == nil || len(e.Files) != 366 {
		t.Fatalf("bleach = %+v", e)
	}
	if len(m.Unmapped) != 1 || m.Unmapped[0].Reason != UnmappedPastSeries {
		t.Errorf("unmapped = %+v, want episode 367 past the series", m.Unmapped)
	}
}

// Movies land on their own entry by the words their titles don't share with the franchise; an
// airing long-runner's axis is open ended.
func TestMapMegapack_MoviesByTitleAndAnOpenEndedSeries(t *testing.T) {
	tv := tvEntry(1, 1999, 0, "One Piece")
	red := otherEntry(2, anilist.MediaFormatMovie, 2022, 1, "One Piece Film: Red", "One Piece Film: Red")
	gold := otherEntry(3, anilist.MediaFormatMovie, 2016, 1, "One Piece Film: Gold", "One Piece Film: Gold")
	first := otherEntry(4, anilist.MediaFormatMovie, 2000, 1, "One Piece (Movie)", "One Piece: The Movie")
	group := anilist.Franchise{SeedID: 1, Entries: []anilist.FranchiseEntry{tv, first, gold, red}}
	files := []nyaa.TorrentFile{
		{Name: "One Piece/Episodes/One Piece - 1071 [1080p].mkv", SizeBytes: 1},
		{Name: "One Piece/Episodes/One Piece - 0001 [1080p].mkv", SizeBytes: 1},
		{Name: "One Piece/Movies/One Piece Film Red (2022) [1080p].mkv", SizeBytes: 5},
		{Name: "One Piece/Movies/One Piece Film Gold (2016) [1080p].mkv", SizeBytes: 4},
		{Name: "One Piece/Movies/One Piece Movie 01 (2000) [1080p].mkv", SizeBytes: 3},
		{Name: "One Piece/Specials/One Piece SP03 [1080p].mkv", SizeBytes: 2},
	}
	m := MapMegapack(files, group, chainOf(tv))
	if e := entryByID(m, 1); e == nil || fmt.Sprint(e.Episodes) != "[1 1071]" {
		t.Errorf("tv = %+v", e)
	}
	for id, size := range map[int]int64{2: 5, 3: 4, 4: 3} {
		if e := entryByID(m, id); e == nil || e.Bytes != size {
			t.Errorf("movie %d = %+v, want its own file (%d bytes)", id, e, size)
		}
	}
	if len(m.Unmapped) != 1 || m.Unmapped[0].Reason != UnmappedNoEntry {
		t.Errorf("unmapped = %+v, want the special no entry names", m.Unmapped)
	}
	if !m.Megapack {
		t.Error("series plus movies is a megapack")
	}
}

// OVAs named after the franchise itself are told apart by their year.
func TestMapMegapack_OVAsByYear(t *testing.T) {
	ova93 := otherEntry(1, anilist.MediaFormatOVA, 1993, 6, "JoJo no Kimyou na Bouken", "JoJo's Bizarre Adventure")
	ova00 := otherEntry(2, anilist.MediaFormatOVA, 2000, 7, "JoJo no Kimyou na Bouken: Adventure", "JoJo's Bizarre Adventure: Adventure")
	tv := tvEntry(3, 2012, 26, "JoJo no Kimyou na Bouken (TV)")
	group := anilist.Franchise{SeedID: 3, Entries: []anilist.FranchiseEntry{ova93, ova00, tv}}
	var files []nyaa.TorrentFile
	for ep := 1; ep <= 6; ep++ {
		files = append(files, nyaa.TorrentFile{Name: fmt.Sprintf("JoJo (Seasons 1-4 + OVAs)/JoJo OVAs [1993]/JoJo OVA %02d.mkv", ep), SizeBytes: 1})
	}
	for ep := 1; ep <= 7; ep++ {
		files = append(files, nyaa.TorrentFile{Name: fmt.Sprintf("JoJo (Seasons 1-4 + OVAs)/JoJo OVAs [2000]/JoJo OVA %02d.mkv", ep), SizeBytes: 1})
	}
	files = append(files, seasonFiles("Season 1", "JoJo", 1, 26)...)
	m := MapMegapack(files, group, chainOf(tv))
	for id, n := range map[int]int{1: 6, 2: 7, 3: 26} {
		if e := entryByID(m, id); e == nil || len(e.Files) != n {
			t.Errorf("entry %d = %+v, want %d files", id, e, n)
		}
	}
}

// A torrent folder named "(S1 + OVA + Specials)" describes the torrent, not each file: its episodes
// stay on the axis, and "Special Training" in an episode's title doesn't make it a special
// (decisions/113).
func TestMapMegapack_TheTorrentFolderAndEpisodeTitlesMarkNoSpecial(t *testing.T) {
	tv := tvEntry(1, 2020, 12, "Show")
	ova := otherEntry(2, anilist.MediaFormatOVA, 2021, 1, "Show OVA", "")
	group := anilist.Franchise{SeedID: 1, Entries: []anilist.FranchiseEntry{tv, ova}}
	var files []nyaa.TorrentFile
	for ep := 1; ep <= 12; ep++ {
		files = append(files, nyaa.TorrentFile{Name: fmt.Sprintf("Show (S1 + OVA + Specials)/Show - %02d.mkv", ep), SizeBytes: 1})
	}
	files = append(files,
		nyaa.TorrentFile{Name: "Show (S1 + OVA + Specials)/Show EP 03 - Special Training!.mkv", SizeBytes: 1},
		nyaa.TorrentFile{Name: "Show (S1 + OVA + Specials)/Show - OVA.mkv", SizeBytes: 1},
	)
	m := MapMegapack(files, group, chainOf(tv))
	if e := entryByID(m, 1); e == nil || len(e.Files) != 13 {
		t.Errorf("tv = %+v, want its 12 episodes and the Special Training episode", e)
	}
	if e := entryByID(m, 2); e == nil || len(e.Files) != 1 {
		t.Errorf("ova = %+v, want the file named OVA", e)
	}
}
