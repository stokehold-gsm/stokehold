//go:build !pro

package discordbot

import (
	"context"
	"net/http"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

// PanelRunner serves the commands that drive a Stokehold panel. They need the
// closed Pro edition, so here there is none.
type PanelRunner interface {
	handles(name string) bool
	run(ctx context.Context, in interaction) string
}

func NewPanel(api *discordsetup.Client, url, token, controlRole string, hc *http.Client) PanelRunner {
	return nil
}

func PanelCommands() []map[string]any { return nil }
