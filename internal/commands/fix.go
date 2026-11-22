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
// run's own earlier requests. Bazarr 1.6.2 writes remove-hi's result in
// place of the video's subtitle of that language that is neither
// hearing-impaired nor forced, whatever the fixed subtitle's flags,
// removing the fixed file. Which rule `fix remove-hi` keeps awaits the
// project owner's decision between G25 as written and one that follows
// where Bazarr writes; until then a subtitle either rule skips is skipped,
// so no fix is sent that either rule forbids.
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
			asWritten := sub.HI && !other.HI && other.Forced == sub.Forced
			whereWritten := !other.HI && !other.Forced
			if asWritten || whereWritten {
				return reporter.ReplacesNonHI
			}
		}
		fixed[k] = true
		return ""
	}
}
