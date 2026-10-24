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

func TestAttributesToRestore(t *testing.T) {
	prev := map[string]string{AttrSIPListen: "customer", "sip.callStatus": "active", "same": "x"}
	cur := map[string]string{AttrSIPListen: "tsumugi-media", "sip.callStatus": "dialing", "same": "x", "new": "y"}
	require.Equal(t, map[string]string{AttrSIPListen: "customer", "sip.callStatus": "active"}, attributesToRestore(prev, cur))
	require.Empty(t, attributesToRestore(nil, cur))
	require.Equal(t, map[string]string{AttrSIPListen: ""}, attributesToRestore(map[string]string{AttrSIPListen: ""}, cur))
}
