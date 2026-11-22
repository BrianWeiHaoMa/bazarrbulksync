package main

import (
	"fmt"
	"strings"
)

// The library the bed provisions: open-licensed films of the Blender
// Foundation (CC BY), with their official subtitles, whose dialogue and
// timings match, so that Bazarr's own sync can succeed on them. The series'
// episodes are copies of the same films, filed under a series whose
// metadata Sonarr can fetch; only its name and numbering are borrowed.

// A download is fetched once into the downloads volume, which take-down
// keeps, and reused by every later bring-up.
type download struct {
	name string // file name in the downloads volume
	url  string
}

var downloads = []download{
	{"Sintel.2010.720p.mkv.zip", "https://download.blender.org/durian/movies/Sintel.2010.720p.mkv.zip"},
	{"tears_of_steel_720p.mov", "https://download.blender.org/demo/movies/ToS/tears_of_steel_720p.mov"},
	{"BigBuckBunny_320x180.mp4.zip", "https://download.blender.org/peach/bigbuckbunny_movies/BigBuckBunny_320x180.mp4.zip"},
	{"sintel_en.srt", "https://durian.blender.org/wp-content/content/subtitles/sintel_en.srt"},
	{"sintel_es.srt", "https://durian.blender.org/wp-content/content/subtitles/sintel_es.srt"},
	{"sintel_de.srt", "https://durian.blender.org/wp-content/content/subtitles/sintel_de.srt"},
	{"sintel_fr.srt", "https://durian.blender.org/wp-content/content/subtitles/sintel_fr.srt"},
	{"TOS-en.srt", "https://download.blender.org/demo/movies/ToS/subtitles/TOS-en.srt"},
	{"TOS-es.srt", "https://download.blender.org/demo/movies/ToS/subtitles/TOS-es.srt"},
	{"TOS-de.srt", "https://download.blender.org/demo/movies/ToS/subtitles/TOS-de.srt"},
	{"TOS-fr-orig.srt", "https://download.blender.org/demo/movies/ToS/subtitles/TOS-fr-orig.srt"},
	{"TOS-it.srt", "https://download.blender.org/demo/movies/ToS/subtitles/TOS-it.srt"},
}

// A prepared video is made once from the downloads by a shell command run in
// the Bazarr image (which carries ffmpeg), and kept beside them.
type prepared struct {
	name    string // file name in the downloads volume
	command string // writes $out from files in $dl; fails the script on error
}

var preparedVideos = []prepared{
	// Video and audio only: the downloaded file's own subtitle streams, if
	// any, would add embedded tracks the plan does not declare.
	{"sintel.mkv", `unzip -p "$dl/Sintel.2010.720p.mkv.zip" '*.mkv' > "$out.src"; ` +
		`ffmpeg -nostdin -loglevel error -y -i "$out.src" -map 0:v -map 0:a -c copy "$out"; rm "$out.src"`},
	{"tos.mkv", `ffmpeg -nostdin -loglevel error -y -i "$dl/tears_of_steel_720p.mov" -map 0:v -map 0:a -c copy "$out"`},
	// One embedded English subtitle track beside the audio track.
	{"tos-embedded.mkv", `ffmpeg -nostdin -loglevel error -y -i "$dl/tears_of_steel_720p.mov" -i "$dl/TOS-en.srt" ` +
		`-map 0:v -map 0:a -map 1:0 -c:v copy -c:a copy -c:s srt -metadata:s:s:0 language=eng "$out"`},
	{"bbb.mp4", `unzip -p "$dl/BigBuckBunny_320x180.mp4.zip" '*.mp4' > "$out"`},
}

// A subtitle file placed beside a video: the video's path with its
// extension replaced by suffix.
type subtitleFile struct {
	suffix   string // ".en.hi.srt"
	source   string // a download
	language string // Bazarr's code2
	hi       bool
	forced   bool
	// Text the checks of fix need, planted in the download's cues; the cue
	// numbers and times stay the download's, so Bazarr's sync still fits.
	edits []edit
	upper bool // every cue text line upper-cased, for fix-uppercase
}

// An edit replaces the first cue text line of the download that reads line
// with text. It is planted for the named fix (F10), which must turn before
// into after; Bazarr's plain rewrite of a file, which follows every fix and
// even an action it does not know, leaves before as it is.
type edit struct {
	line, text    string
	fix           string
	before, after string
}

// A video file of the library.
type video struct {
	path     string // under the media volume, as every service sees it
	source   string // a prepared video
	embedded bool   // has an embedded subtitle track
	subs     []subtitleFile
}

// A movie Radarr adds by its TMDB ID.
type movie struct {
	tmdbID int
	folder string
	video  video
}

// The series Sonarr adds by its TVDB ID, and its episodes.
type series struct {
	tvdbID   int
	folder   string
	episodes []episode
}

type episode struct {
	season, number int
	video          video
}

const (
	moviesRoot = "/media/movies"
	tvRoot     = "/media/tv"
)

// The movies, in the order Bazarr lists them (by Radarr's sort title).
var movies = []movie{
	{10378, moviesRoot + "/Big Buck Bunny (2008)", video{
		// No speech, and no external subtitle at all.
		path: moviesRoot + "/Big Buck Bunny (2008)/Big Buck Bunny (2008).mp4", source: "bbb.mp4"}},
	{45745, moviesRoot + "/Sintel (2010)", video{
		path: moviesRoot + "/Sintel (2010)/Sintel (2010).mkv", source: "sintel.mkv",
		subs: []subtitleFile{
			{suffix: ".en.srt", source: "sintel_en.srt", language: "en", edits: []edit{
				// "afier" is on Bazarr's English OCR list.
				{"It has shed much innocent blood.", "It has shed much innocent blood afier all.",
					"ocr-fixes", "blood afier all.", "blood after all."}}},
			// A style tag in a title other than the one remove-tags is run on.
			{suffix: ".es.srt", source: "sintel_es.srt", language: "es", edits: []edit{
				{"Gracias.", "<i>Gracias.</i>", "remove-tags", "<i>Gracias.</i>", "Gracias."}}},
			{suffix: ".de.srt", source: "sintel_de.srt", language: "de", edits: []edit{
				{"Diese Klinge birgt eine finstere", ">> Diese Klinge birgt eine finstere",
					"common-fixes", ">> Diese Klinge", "Diese Klinge birgt eine finstere"}}},
			// Hearing-impaired, beside a non-HI subtitle of its language.
			{suffix: ".en.hi.srt", source: "sintel_en.srt", language: "en", hi: true},
			{suffix: ".en.forced.srt", source: "sintel_en.srt", language: "en", forced: true},
		}}},
	{133701, moviesRoot + "/Tears of Steel (2012)", video{
		path: moviesRoot + "/Tears of Steel (2012)/Tears of Steel (2012).mkv", source: "tos-embedded.mkv", embedded: true,
		subs: []subtitleFile{
			{suffix: ".en.srt", source: "TOS-en.srt", language: "en", upper: true, edits: []edit{
				// Kept as it is, then upper-cased with the rest of the file.
				{"You're a jerk, Thom.", "You're a jerk, Thom.", "fix-uppercase", "YOU'RE A JERK, THOM.", "You're a jerk, thom."}}},
			{suffix: ".es.srt", source: "TOS-es.srt", language: "es"},
			{suffix: ".fr.srt", source: "TOS-fr-orig.srt", language: "fr", edits: []edit{
				{"Celia, on doit poursuivre nos passions;", "Celia, on doit poursuivre nos passions; 🚀",
					"remove-emoji", "🚀", "Celia, on doit poursuivre nos passions;"}}},
			// Hearing-impaired, with no non-HI subtitle of its language.
			{suffix: ".it.hi.srt", source: "TOS-it.srt", language: "it", hi: true, edits: []edit{
				{"Sei un idiota, Thom.", "CELIA: Sei un idiota, Thom.", "remove-hi", "CELIA: Sei", "Sei un idiota, Thom."},
				{"Senti Celia, dobbiamo seguire le nostre passioni;", "[sospira] Senti Celia, dobbiamo seguire le nostre passioni;",
					"remove-hi", "[sospira]", "Senti Celia, dobbiamo seguire le nostre passioni;"}}},
		}}},
}

// Every file of the series holds a style tag, for remove-tags run on its
// title. Only italic and underline: Bazarr's plain rewrite drops bold and
// font tags by itself, so their absence after a fix would show nothing.
var theSeries = series{75932, tvRoot + "/Fawlty Towers", []episode{
	{1, 1, video{path: tvRoot + "/Fawlty Towers/Season 01/Fawlty Towers - S01E01.mkv", source: "sintel.mkv",
		subs: []subtitleFile{
			{suffix: ".en.srt", source: "sintel_en.srt", language: "en", edits: []edit{
				{"Thank you.", "<i>Thank you.</i>", "remove-tags", "<i>Thank you.</i>", "Thank you."}}},
			{suffix: ".fr.srt", source: "sintel_fr.srt", language: "fr", edits: []edit{
				{"Merci.", "<u>Merci.</u>", "remove-tags", "<u>Merci.</u>", "Merci."}}},
		}}},
	{1, 2, video{path: tvRoot + "/Fawlty Towers/Season 01/Fawlty Towers - S01E02.mkv", source: "tos.mkv",
		subs: []subtitleFile{
			{suffix: ".en.srt", source: "TOS-en.srt", language: "en", edits: []edit{
				{"You're a jerk, Thom.", "<i>You're a jerk</i>, Thom.", "remove-tags", "<i>You're a jerk</i>, Thom.", "You're a jerk, Thom."}}},
			{suffix: ".es.srt", source: "TOS-es.srt", language: "es", edits: []edit{
				{"Eres un imbécil, Thom.", "<i>Eres un imbécil, Thom.</i>", "remove-tags", "<i>Eres un imbécil, Thom.</i>", "Eres un imbécil, Thom."}}},
		}}},
	{2, 1, video{path: tvRoot + "/Fawlty Towers/Season 02/Fawlty Towers - S02E01.mkv", source: "tos.mkv",
		subs: []subtitleFile{
			{suffix: ".en.srt", source: "TOS-en.srt", language: "en", edits: []edit{
				{"You're a jerk, Thom.", "<u>You're a jerk, Thom.</u>", "remove-tags", "<u>You're a jerk, Thom.</u>", "You're a jerk, Thom."}}},
			{suffix: ".de.srt", source: "TOS-de.srt", language: "de", edits: []edit{
				{"Du bist ein Vollidiot, Thom.", "<i>Du bist ein Vollidiot, Thom.</i>", "remove-tags", "<i>Du bist ein Vollidiot, Thom.</i>", "Du bist ein Vollidiot, Thom."}}},
		}}},
}}

// subtitlePath is where a subtitle file of v is placed.
func subtitlePath(v video, s subtitleFile) string {
	base := v.path[:strings.LastIndex(v.path, ".")]
	return base + s.suffix
}

// videos returns every video file of the library.
func videos() []video {
	var all []video
	for _, m := range movies {
		all = append(all, m.video)
	}
	for _, e := range theSeries.episodes {
		all = append(all, e.video)
	}
	return all
}

// prepareScript is the shell script, run in the Bazarr image with the
// downloads volume at /downloads and an empty media volume at /media, that
// fetches what is not yet downloaded, prepares the videos, and places the
// library. A download is complete once renamed into place, so an
// interrupted bring-up never leaves a partial file that a later one trusts.
func prepareScript() string {
	var b strings.Builder
	b.WriteString("set -eu\ndl=/downloads\n")
	for _, d := range downloads {
		fmt.Fprintf(&b, "if [ ! -f %[1]s ]; then echo 'downloading %[2]s' >&2; "+
			"curl -fsSL --retry 3 -o %[1]s.part %[3]s; mv %[1]s.part %[1]s; fi\n",
			quote("/downloads/"+d.name), d.name, quote(d.url))
	}
	for _, p := range preparedVideos {
		// The partial file keeps its extension, from which ffmpeg picks the format.
		fmt.Fprintf(&b, "if [ ! -f %[1]s ]; then echo 'preparing %[3]s' >&2; out=%[2]s; %[4]s; mv %[2]s %[1]s; fi\n",
			quote("/downloads/"+p.name), quote("/downloads/partial."+p.name), p.name, p.command)
	}
	for _, v := range videos() {
		fmt.Fprintf(&b, "mkdir -p %s\ncp /downloads/%s %s\n", quote(dir(v.path)), quote(v.source), quote(v.path))
		for _, s := range v.subs {
			placeSubtitle(&b, v, s)
		}
	}
	b.WriteString("chown -R abc:abc /media\n")
	return b.String()
}

// placeSubtitle writes the commands that copy s's download beside v and
// plant its text. The bring-up fails when an edit finds no line to replace,
// or when the placed file's line count, cue numbers, or cue times differ
// from the download's: planting changes cue text only.
func placeSubtitle(b *strings.Builder, v video, s subtitleFile) {
	src, dst := quote("/downloads/"+s.source), quote(subtitlePath(v, s))
	fmt.Fprintf(b, "cp %s %s\n", src, dst)
	if len(s.edits) == 0 && !s.upper {
		return
	}
	for _, e := range s.edits {
		fmt.Fprintf(b, "LINE=%s TEXT=%s awk %s %s > /tmp/planted || exit 1; cat /tmp/planted > %s\n",
			quote(e.line), quote(e.text), quote(editProgram), dst, dst)
	}
	if s.upper {
		fmt.Fprintf(b, "awk %s %s > /tmp/planted || exit 1; cat /tmp/planted > %s\n", quote(upperProgram), dst, dst)
	}
	// Cue numbers and times: a line holding -->, or digits alone before an
	// optional carriage return.
	fmt.Fprintf(b, "grep -E -e '-->' -e '^[0-9]+.?$' %s > /tmp/times.src; grep -E -e '-->' -e '^[0-9]+.?$' %s > /tmp/times.dst\n", src, dst)
	fmt.Fprintf(b, "[ \"$(wc -l < %s)\" = \"$(wc -l < %s)\" ] && cmp -s /tmp/times.src /tmp/times.dst || { echo %s >&2; exit 1; }\n",
		src, dst, quote("planting text changed the cues of "+subtitlePath(v, s)))
}

// editProgram is the awk program of an edit: it replaces the first line
// reading $LINE, its carriage return aside, with $TEXT, and fails when no
// line does.
const editProgram = `{ l = $0; cr = ""; if (sub(/\r$/, "", l)) cr = "\r" }
!done && l == ENVIRON["LINE"] { print ENVIRON["TEXT"] cr; done = 1; next }
{ print }
END { if (!done) { print "no line reads: " ENVIRON["LINE"] > "/dev/stderr"; exit 1 } }`

// upperProgram upper-cases every line that is neither a cue number nor a
// cue's times.
const upperProgram = `{ l = $0; cr = ""; if (sub(/\r$/, "", l)) cr = "\r"
if (l !~ /-->/ && l !~ /^[0-9]*$/) l = toupper(l); print l cr }`

func dir(path string) string { return path[:strings.LastIndex(path, "/")] }

// quote quotes s for the shell.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
