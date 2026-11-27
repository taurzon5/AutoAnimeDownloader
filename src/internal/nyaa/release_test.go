package nyaa

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Names in the shapes measured for the release-filters spec (2026-11-23): SubsPlease says almost
// nothing, Erai-raws writes AVC, ToonsHub writes DUAL and a glued AAC2.0, scene names end in -GROUP.
func TestParseRelease(t *testing.T) {
	for _, tc := range []struct {
		name string
		want Release
	}{
		{"[SubsPlease] Sousou no Frieren - 07 (1080p) [ABCD1234].mkv", Release{
			Group: "subsplease", Resolution: "1080p", Source: "web-dl", Container: "mkv", Version: 1,
			AudioLanguages: []string{"ja"}, SubtitleLanguages: []string{"en"},
			Assumed: []string{"source", "audio_languages", "subtitle_languages"},
		}},
		{"[Erai-raws] Kaiju No. 8 - 07 [1080p CR WEB-DL AVC AAC][MultiSub][ABCD1234]", Release{
			Group: "erai-raws", Resolution: "1080p", Source: "web-dl", Service: "cr", VideoCodec: "h.264",
			AudioCodec: "aac", Version: 1, AudioLanguages: []string{"ja"}, SubtitleLanguages: []string{"en", "multi"},
			Assumed: []string{"audio_languages"},
		}},
		{"[ToonsHub] Dandadan S02E07 1080p NF WEB-DL DUAL AAC2.0 H.264 (Multi-Subs)", Release{
			Group: "toonshub", Resolution: "1080p", Source: "web-dl", Service: "nf", VideoCodec: "h.264",
			AudioCodec: "aac", AudioChannels: "2.0", Version: 1,
			AudioLanguages: []string{"ja", "en"}, SubtitleLanguages: []string{"en", "multi"},
		}},
		{"Kaiju.No.8.S02E07.1080p.CR.WEB-DL.AAC2.0.H.264-VARYG.mkv", Release{
			Group: "varyg", Resolution: "1080p", Source: "web-dl", Service: "cr", VideoCodec: "h.264",
			AudioCodec: "aac", AudioChannels: "2.0", Container: "mkv", Version: 1,
			AudioLanguages: []string{"ja"}, SubtitleLanguages: []string{"en"},
			Assumed: []string{"audio_languages", "subtitle_languages"},
		}},
		{"[Judas] One Piece - 1100 [1080p][HEVC x265 10bit][Multi-Subs] v2", Release{
			Group: "judas", Resolution: "1080p", Source: "web-dl", VideoCodec: "hevc", BitDepth: 10, Version: 2,
			AudioLanguages: []string{"ja"}, SubtitleLanguages: []string{"en", "multi"},
			Assumed: []string{"source", "audio_languages"},
		}},
		{"[EMBER] Mushoku Tensei S01 [1080p] [HEVC WEBRip] (Dual Audio) (Batch)", Release{
			Group: "ember", Resolution: "1080p", Source: "webrip", VideoCodec: "hevc", Version: 1, Batch: true,
			AudioLanguages: []string{"ja", "en"}, SubtitleLanguages: []string{"en"},
			Assumed: []string{"subtitle_languages"},
		}},
		{"[Group] Show - 05 [1080p][BD][FLAC 5.1][PT-BR] REPACK", Release{
			Group: "group", Resolution: "1080p", Source: "bd", AudioCodec: "flac", AudioChannels: "5.1",
			Version: 1, Repack: true, AudioLanguages: []string{"ja"}, SubtitleLanguages: []string{"pt-br"},
			Assumed: []string{"audio_languages"},
		}},
		// A hardsub says nothing about the subtitles' language: none is assumed.
		{"[Group] Show - 03 [English Dub][720p][HC] Uncensored", Release{
			Group: "group", Resolution: "720p", Source: "web-dl", Version: 1, Hardsub: true, Uncensored: true,
			AudioLanguages: []string{"en"}, Assumed: []string{"source"},
		}},
		// The (Group) form; Hi10 is 10-bit.
		{"(Hi10) Gosick - 01 (BD 720p AAC)", Release{
			Group: "hi10", Resolution: "720p", Source: "bd", BitDepth: 10, AudioCodec: "aac", Version: 1,
			AudioLanguages: []string{"ja"}, SubtitleLanguages: []string{"en"},
			Assumed: []string{"audio_languages", "subtitle_languages"},
		}},
		// "MULTI-SUBS" is a subtitle marker: its MULTI isn't multi audio.
		{"[Group] Show - 02 [1080p][MULTI-SUBS]", Release{
			Group: "group", Resolution: "1080p", Source: "web-dl", Version: 1,
			AudioLanguages: []string{"ja"}, SubtitleLanguages: []string{"en", "multi"},
			Assumed: []string{"source", "audio_languages"},
		}},
		{"[Group] Show - 01 [1080p][EAC3 5.1][MULTi]", Release{
			Group: "group", Resolution: "1080p", Source: "web-dl", AudioCodec: "eac3", AudioChannels: "5.1", Version: 1,
			AudioLanguages: []string{"ja", "en", "multi"}, SubtitleLanguages: []string{"en"},
			Assumed: []string{"source", "subtitle_languages"},
		}},
	} {
		if got := ParseRelease(tc.name); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s\n got  %+v\n want %+v", tc.name, got, tc.want)
		}
	}
}

// The extractor fixes the spec planned: AVC is H.264, Opus is read, a (Group) name has a group, and
// a scene name's -GROUP is its group, but WEB-DL's DL isn't. The ranking's audio reading is
// otherwise as it was: E-AC-3 still reads as AC3 there and DTS as nothing (decisions/150), while
// ParseRelease reads them right.
func TestExtractorFixes(t *testing.T) {
	if got := ExtractCodec("[Erai-raws] Show - 01 [1080p CR WEB-DL AVC AAC]"); got != "H.264" {
		t.Errorf("AVC: codec %q, want H.264", got)
	}
	if got := ExtractCodec("[Group] Show - 01 [HEVC]"); got != "HEVC" {
		t.Errorf("HEVC: codec %q", got)
	}
	for name, want := range map[string]string{
		"[Group] Show - 01 [1080p Opus]":   "Opus",
		"[Group] Show - 01 [Opus][AAC]":    "AAC",
		"[Group] Show - 01 [EAC3 5.1]":     "AC3",
		"[Group] Show - 01 [AC3]":          "AC3",
		"[Group] Show - 01 [DTS][AAC 2.0]": "AAC",
	} {
		if got := extractAudio(name); got != want {
			t.Errorf("%s: ranking audio %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"[Group] Show - 01 [EAC3 5.1]":     "eac3",
		"[Group] Show - 01 [E-AC-3]":       "eac3",
		"[Group] Show - 01 [DTS][AAC 2.0]": "dts",
		"[Group] Show - 01 [Opus]":         "opus",
	} {
		if got := ParseRelease(name).AudioCodec; got != want {
			t.Errorf("%s: release audio %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"[SubsPlease] Show - 07 (1080p) [HASH].mkv":          "subsplease",
		"(Hi10)_Show_-_01":                                   "hi10",
		"Show.S02E07.1080p.CR.WEB-DL.AAC2.0.H.264-VARYG.mkv": "varyg",
		"Show S02E07 1080p WEB-DL":                           "",
		"Show - 07 (1080p)":                                  "",
		"Show S01 1080p BluRay x265-Ghost":                   "ghost",
	} {
		if got := extractFansub(name); got != want {
			t.Errorf("%s: group %q, want %q", name, got, want)
		}
	}
}

// The row's upload time comes from the date cell's timestamp (else its text), and its class says
// trusted (success) or remake (danger).
func TestWithRowInfo(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<table><tbody>
		<tr class="success"><td></td><td></td><td></td><td></td><td class="text-center" data-timestamp="1700000000">2023-11-14 22:13</td></tr>
		<tr class="danger"><td></td><td></td><td></td><td></td><td>2020-01-02 10:00</td></tr>
		<tr class="default"><td></td><td></td><td></td><td></td><td></td></tr>
	</tbody></table>`))
	if err != nil {
		t.Fatal(err)
	}
	var got []TorrentResult
	doc.Find("tr").Each(func(_ int, s *goquery.Selection) {
		got = append(got, withRowInfo(TorrentResult{}, s, s.Find("td")))
	})
	if !got[0].Trusted || got[0].Remake || !got[0].Date.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("trusted row: %+v", got[0])
	}
	if got[1].Trusted || !got[1].Remake || !got[1].Date.Equal(time.Date(2020, 1, 2, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("remake row: %+v", got[1])
	}
	if got[2].Trusted || got[2].Remake || !got[2].Date.IsZero() {
		t.Errorf("plain row: %+v", got[2])
	}
}

// An underscore-joined name reads its tags as if the underscores were spaces; the group keeps the
// raw name.
func TestParseRelease_UnderscoreJoinedName(t *testing.T) {
	r := ParseRelease("[Some_Group]_Show_-_01_[CR_1080p_WEB-DL_10bit_v2_DTS]")
	if r.Group != "some_group" || r.Service != "cr" || r.Resolution != "1080p" || r.BitDepth != 10 || r.Version != 2 {
		t.Errorf("ParseRelease = %+v", r)
	}
}
