package cli

import (
	"errors"
	"testing"

	"github.com/Keldrik/pcast/internal/model"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestHumanRenderersPropagateOutputErrors(t *testing.T) {
	cases := []struct {
		name string
		run  func() error
	}{
		{"add", func() error {
			return RenderAdd(failingWriter{}, false, model.AddResult{})
		}},
		{"list", func() error {
			return RenderList(failingWriter{}, false, model.ListResult{Podcasts: []model.Podcast{{ID: 1, Title: "T"}}})
		}},
		{"latest", func() error {
			return RenderLatest(failingWriter{}, false, model.LatestResult{})
		}},
		{"episodes", func() error {
			return RenderEpisodes(failingWriter{}, false, model.EpisodesResult{})
		}},
		{"episode", func() error {
			return RenderEpisode(failingWriter{}, false, model.EpisodeResult{})
		}},
		{"doctor", func() error {
			return RenderDoctor(failingWriter{}, false, model.DoctorResult{})
		}},
		{"version", func() error {
			return RenderVersion(failingWriter{}, false, model.VersionInfo{})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("expected output error")
			}
		})
	}
}
