package sip

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListenAllows(t *testing.T) {
	for _, c := range []struct {
		listen   string
		identity string
		want     bool
	}{
		{"", "agent", true},
		{"  ", "agent", true},
		{"agent", "agent", true},
		{"agent", "media", false},
		{"media, agent", "agent", true},
		{"media,agent", "caller", false},
		{"media,", "", false},
		{"agentx", "agent", false},
	} {
		require.Equal(t, c.want, listenAllows(c.listen, c.identity), "listen %q identity %q", c.listen, c.identity)
	}
}
