// Copyright 2026 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sip

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/livekit/media-sdk/tones"
	"github.com/livekit/sipgo/sip"
)

const statusSessionProgress = sip.StatusCode(183)

// ringbackTones are the ringback tones available for inbound early media.
var ringbackTones = map[string][]tones.Tone{
	// Japan: 400Hz with 15Hz modulation, 1s on, 2s off.
	"jp": {{Freq: []tones.Hz{400, 415}, Dur: time.Second, Silence: 2 * time.Second}},
	// ETSI: 425Hz, 1s on, 4s off.
	"etsi": tones.ETSIRinging,
}

func ringbackVolume(v int) int16 {
	if v <= 0 || v > math.MaxInt16 {
		return math.MaxInt16 / 2
	}
	return int16(v)
}

// startEarlyMedia sends 183 Session Progress with the SDP answer and plays the configured
// ringback tone to the caller. The returned func stops the tone and waits until it is no longer
// writing to the media port, so the room audio can be attached right after it returns.
// It is a no-op when inbound early media is not configured.
func (c *inboundCall) startEarlyMedia(ctx context.Context, sdpAnswer []byte) (stop func()) {
	conf := c.s.conf.InboundEarlyMedia
	if conf == nil {
		return func() {}
	}
	tone, ok := ringbackTones[conf.Tone]
	if !ok {
		return func() {}
	}
	c.mmu.Lock()
	mp := c.media
	c.mmu.Unlock()
	if mp == nil {
		return func() {}
	}
	if !c.cc.SendProgress(sdpAnswer) {
		return func() {}
	}
	c.log().Infow("sent session progress, playing ringback", "tone", conf.Tone)

	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := tones.Play(rctx, mp.GetOutboundAudioWriter(), ringbackVolume(conf.Volume), tone)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			c.log().Infow("cannot play ringback", "error", err)
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}
