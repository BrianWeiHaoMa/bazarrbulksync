package commands

import (
	"context"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
)

// fixCommand asks Bazarr to apply fix to each selected subtitle (G24);
// remove-hi skips the subtitles G25 names.
func fixCommand(fix bazarr.Fix) changeCommand {
	cmd := changeCommand{
		done:     reporter.Applied,
		wouldDo:  reporter.WouldApply,
		question: "Apply " + invocation.FixName(fix) + " to them",
		send: func(ctx context.Context, c *bazarr.Client, sub bazarr.Subtitle) (bazarr.ChangeResult, error) {
			return c.Fix(ctx, sub, fix)
		},
	}
	if fix == bazarr.FixRemoveHI {
		cmd.skip = removeHISkip
	}
	return cmd
}

// videoLanguage is one language of one video: a movie or an episode.
type videoLanguage struct {
	videoType bazarr.VideoType
	videoID   int // the Radarr movie ID or the Sonarr episode ID
	language  string
}

func videoLanguageOf(s bazarr.Subtitle) videoLanguage {
	if s.Type == bazarr.TypeMovie {
		return videoLanguage{s.Type, s.MovieID, s.Language}
	}
	return videoLanguage{s.Type, s.EpisodeID, s.Language}
}

// removeHISkip is G25's rule, judged from the library as read and from the
// subtitles this run has already let through. Bazarr 1.6.2 writes
// remove-hi's result in place of the video's subtitle of that language that
// is neither hearing-impaired nor forced, whatever the fixed subtitle's
// flags, removing the fixed file; so a subtitle is skipped when its video
// has such a subtitle of its language, or when the run has let an earlier
// subtitle of its video and language through, whatever Bazarr answered.
func removeHISkip(lib bazarr.Library) func(bazarr.Subtitle) reporter.SkipReason {
	byVideoLanguage := map[videoLanguage][]bazarr.Subtitle{}
	for _, s := range lib.Subtitles() {
		k := videoLanguageOf(s)
		byVideoLanguage[k] = append(byVideoLanguage[k], s)
	}
	// fixed holds each video and language this run has let a fix through
	// for: Bazarr's result now sits where a second fix would write.
	fixed := map[videoLanguage]bool{}
	return func(sub bazarr.Subtitle) reporter.SkipReason {
		k := videoLanguageOf(sub)
		if fixed[k] {
			return reporter.ReplacesNonHI
		}
		for _, other := range byVideoLanguage[k] {
			if other.Path == sub.Path {
				continue
			}
			if !other.HI && !other.Forced {
				return reporter.ReplacesNonHI
			}
		}
		fixed[k] = true
		return ""
	}
}
