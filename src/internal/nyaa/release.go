package nyaa

import (
	"regexp"
	"slices"
	"strings"
)

// Release is what a release name says about the torrent: its group and its tags, in the release
// filters' vocabulary (lowercase: "web-dl", "h.264", "aac", "2.0", ISO 639-1 languages). One parse
// read by the anime page's torrent cards today, and by the filters and the picker later (the
// release-filters spec §1).
//
// A field the name leaves out stays empty, except the few with a smart default: those are filled
// and listed in Assumed, so a reader can say "assumed" instead of presenting a guess as read.
type Release struct {
	Group             string   `json:"group,omitempty"`
	Resolution        string   `json:"resolution,omitempty"`
	Source            string   `json:"source,omitempty"`
	Service           string   `json:"service,omitempty"`
	VideoCodec        string   `json:"video_codec,omitempty"`
	BitDepth          int      `json:"bit_depth,omitempty"`
	AudioCodec        string   `json:"audio_codec,omitempty"`
	AudioChannels     string   `json:"audio_channels,omitempty"`
	AudioLanguages    []string `json:"audio_languages,omitempty"`
	SubtitleLanguages []string `json:"subtitle_languages,omitempty"`
	Hardsub           bool     `json:"hardsub,omitempty"`
	Container         string   `json:"container,omitempty"`
	// Version is 1 unless the name says v2…v9; Repack is REPACK or PROPER.
	Version    int  `json:"version,omitempty"`
	Repack     bool `json:"repack,omitempty"`
	Uncensored bool `json:"uncensored,omitempty"`
	Batch      bool `json:"batch,omitempty"`
	// Assumed names the fields filled by a smart default rather than read from the name:
	// "source", "audio_languages", "subtitle_languages".
	Assumed []string `json:"assumed,omitempty"`
}

// The smart defaults, for a release of Nyaa's English-translated category (c=1_2, the only one the
// app searches): no source marker reads as WEB-DL, as the ranking already does (decisions/107); no
// audio marker is the Japanese track; no subtitle marker is English subtitles.
const (
	defaultSource           = "web-dl"
	defaultAudioLanguage    = "ja"
	defaultSubtitleLanguage = "en"
)

var (
	// The services, matched case-sensitively as whole words: "CR" and "NF" in lowercase are too
	// common inside other words to read.
	reServices = []struct {
		re      *regexp.Regexp
		service string
	}{
		{regexp.MustCompile(`\bCR\b|(?i:\bcrunchyroll\b)`), "cr"},
		{regexp.MustCompile(`\bNF\b|(?i:\bnetflix\b)`), "nf"},
		{regexp.MustCompile(`\bAMZN\b|(?i:\bamazon\b)`), "amzn"},
		{regexp.MustCompile(`\bDSNP\b|(?i:\bdisney\+)`), "dsnp"},
		{regexp.MustCompile(`\bADN\b`), "adn"},
		{regexp.MustCompile(`(?i)\bb-global\b|\bbilibili\b`), "b-global"},
		{regexp.MustCompile(`\bHIDI\b|(?i:\bhidive\b)`), "hidi"},
		{regexp.MustCompile(`(?i)\bhulu\b`), "hulu"},
	}

	reBitDepth10 = regexp.MustCompile(`(?i)\b10[-\s]?bits?\b|\bhi10p?\b`)
	reBitDepth8  = regexp.MustCompile(`(?i)\b8[-\s]?bits?\b`)

	// The channel count, also glued to its codec ("AAC2.0", "DDP5.1"). The digit before the dot must
	// not be part of a longer number, so "H.264" and "1080.0" read nothing.
	reAudioChannels = regexp.MustCompile(`(?:^|[^\d.])([257]\.[01])(?:[^\d]|$)`)

	// DUAL and MULTi are scene tags, matched in their own case: "Dual" or "Multi" can be a title word.
	reDualAudio  = regexp.MustCompile(`(?i:\bdual[-\s]?audio\b)|\bDUAL\b`)
	reMultiAudio = regexp.MustCompile(`(?i:\bmulti[-\s]?audio\b)|\bMULTi\b|\bMULTI\b`)
	reDub        = regexp.MustCompile(`(?i)\b(?:english[-\s]?)?dub(?:bed)?\b`)
	reJapanese   = regexp.MustCompile(`(?i)\bJPN\b|\bjapanese[-\s]audio\b`)

	// A subtitle marker that says nothing about the languages beyond "several".
	reMultiSub   = regexp.MustCompile(`(?i)\bmulti[-\s]?subs?\b`)
	reEnglishSub = regexp.MustCompile(`(?i)\be-?subs?\b|\beng(?:lish)?[-\s]?subs?\b`)
	reHardsub    = regexp.MustCompile(`(?i:\bhard[-\s]?sub(?:bed|s)?\b)|\bHC\b`)
	// The subtitle languages of the subtitle-language spec (decisions are in that spec); en is the
	// default and reEnglishSub, so it isn't here. No \b on the hyphenated aliases: "[PT-BR]".
	subtitleAliases = []struct {
		lang    string
		aliases []string
	}{
		{"pt-br", []string{"pt-br", "ptbr", "pt_br", "legendado", "portuguese", "português", "portugues"}},
		{"es-la", []string{"es-la", "esla", "castellano", "español", "espanol", "spanish", "subtitulado"}},
		{"fr", []string{"vostfr", "subfrench"}},
	}

	reContainer = regexp.MustCompile(`(?i)\.(mkv|mp4)\s*$|\b(MKV|MP4)\b`)
	reVersion   = regexp.MustCompile(`(?i)(?:\d|\b)v([2-9])\b`)
	reRepack    = regexp.MustCompile(`(?i)\b(?:repack|proper)\b`)
)

// canonicalSources maps ExtractSource's labels to the filters' vocabulary.
var canonicalSources = map[string]string{
	"BDRemux": "bdremux", "BDRip": "bdrip", "BD": "bd", "WEB-DL": "web-dl", "WEBRip": "webrip",
	"HDTV": "hdtv", "TV": "tv", "DVD": "dvd",
}

// releaseAudioCodecs reads the audio codec in the filters' vocabulary. It is its own list, not
// extractAudio's, because the ranking reads extractAudio: there E-AC-3 still matches "AC3" and DTS
// isn't read, and changing that would move picks (decisions/150). Here E-AC-3 is Dolby Digital Plus
// (eac3) and DTS-HD a DTS track. First match wins, in this order.
var releaseAudioCodecs = []struct {
	re    *regexp.Regexp
	codec string
}{
	{regexp.MustCompile(`(?i)FLAC`), "flac"},
	{regexp.MustCompile(`(?i)DTS-HD|\bDTS\b`), "dts"},
	{regexp.MustCompile(`(?i)TrueHD`), "truehd"},
	{regexp.MustCompile(`(?i)DDP|Dolby\s*Digital\s*Plus|\bE-?AC-?3`), "eac3"},
	{regexp.MustCompile(`(?i)AAC`), "aac"},
	{regexp.MustCompile(`(?i)AC3|Dolby\s*Digital`), "ac3"},
	{regexp.MustCompile(`(?i)MP3`), "mp3"},
	{regexp.MustCompile(`(?i)\bOpus\b`), "opus"},
}

func releaseAudioCodec(name string) string {
	for _, a := range releaseAudioCodecs {
		if a.re.MatchString(name) {
			return a.codec
		}
	}
	return ""
}

// ParseRelease reads a release name. It never fails: what it can't read stays empty.
func ParseRelease(name string) Release {
	r := Release{
		Group:      extractFansub(name),
		Resolution: ExtractResolution(name),
		Source:     canonicalSources[ExtractSource(name)],
		VideoCodec: strings.ToLower(ExtractCodec(name)),
		AudioCodec: releaseAudioCodec(name),
		Version:    1,
		Repack:     reRepack.MatchString(name),
		Uncensored: isUncensored(name),
		Batch:      IsBatch(name),
	}
	for _, s := range reServices {
		if s.re.MatchString(name) {
			r.Service = s.service
			break
		}
	}
	switch {
	case reBitDepth10.MatchString(name):
		r.BitDepth = 10
	case reBitDepth8.MatchString(name):
		r.BitDepth = 8
	}
	if m := reAudioChannels.FindStringSubmatch(name); m != nil {
		r.AudioChannels = m[1]
	}
	if m := reContainer.FindStringSubmatch(name); m != nil {
		r.Container = strings.ToLower(m[1] + m[2])
	}
	if m := reVersion.FindStringSubmatch(name); m != nil {
		r.Version = int(m[1][0] - '0')
	}

	r.AudioLanguages = audioLanguages(name)
	r.SubtitleLanguages, r.Hardsub = subtitleLanguages(name)

	if r.Source == "" {
		r.Source = defaultSource
		r.Assumed = append(r.Assumed, "source")
	}
	if len(r.AudioLanguages) == 0 {
		r.AudioLanguages = []string{defaultAudioLanguage}
		r.Assumed = append(r.Assumed, "audio_languages")
	}
	if len(r.SubtitleLanguages) == 0 && !r.Hardsub {
		r.SubtitleLanguages = []string{defaultSubtitleLanguage}
		r.Assumed = append(r.Assumed, "subtitle_languages")
	}
	return r
}

// audioLanguages reads the audio markers: dual audio is Japanese and English, multi audio adds
// "multi" (more, unnamed), a dub is English, JPN is Japanese. A "Multi-Subs" marker is about
// subtitles and doesn't count.
func audioLanguages(name string) []string {
	var out []string
	add := func(langs ...string) {
		for _, l := range langs {
			if !slices.Contains(out, l) {
				out = append(out, l)
			}
		}
	}
	if reDualAudio.MatchString(name) {
		add("ja", "en")
	}
	// "MULTI-Subs" is a subtitle marker: taken out first, so its "MULTI" isn't read as audio.
	if reMultiAudio.MatchString(reMultiSub.ReplaceAllString(name, " ")) {
		add("ja", "en", "multi")
	}
	if reDub.MatchString(name) {
		add("en")
	}
	if reJapanese.MatchString(name) {
		add("ja")
	}
	return out
}

// subtitleLanguages reads the subtitle markers: "MultiSub" is English and more ("multi"), an
// English subtitle marker is English, the subtitle spec's aliases name their language, and a
// hardsub marker sets hardsub.
func subtitleLanguages(name string) ([]string, bool) {
	var out []string
	if reMultiSub.MatchString(name) {
		out = append(out, "en", "multi")
	} else if reEnglishSub.MatchString(name) {
		out = append(out, "en")
	}
	lower := strings.ToLower(name)
	for _, a := range subtitleAliases {
		for _, alias := range a.aliases {
			if strings.Contains(lower, alias) {
				out = append(out, a.lang)
				break
			}
		}
	}
	return out, reHardsub.MatchString(name)
}
