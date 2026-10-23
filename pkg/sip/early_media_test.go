package sip

import (
	"context"
	"math"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"

	msdk "github.com/livekit/media-sdk"
	"github.com/livekit/media-sdk/dtmf"
	"github.com/livekit/media-sdk/g711"
	"github.com/livekit/media-sdk/sdp"
	"github.com/livekit/sipgo/sip"

	"github.com/livekit/sip/pkg/config"
)

// earlyMediaInvite sends an INVITE whose SDP offer points RTP at a local UDP socket, so the test can
// observe the audio sent to the caller before the call is answered.
func earlyMediaInvite(t *testing.T, st *serviceTest) (*sipUADialogTest, *sip.Request, sip.ClientTransaction, *net.UDPConn) {
	t.Helper()
	rtpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rtpConn.Close() })
	rtpPort := rtpConn.LocalAddr().(*net.UDPAddr).Port

	// Like the carrier: PCMU and telephone-event only.
	codecs := msdk.NewCodecSet()
	codecs.SetEnabledMap(map[string]bool{g711.ULawSDPNameOnly: true, dtmf.SDPNameOnly: true})
	offer, err := sdp.NewOfferWith(codecs, netip.MustParseAddr("127.0.0.1"), rtpPort, sdp.EncryptionNone)
	require.NoError(t, err)
	offerData, err := offer.SDP.Marshal()
	require.NoError(t, err)

	call := newTestCall(st.TestUA, false)
	req, localSDP, err := call.Invite(offerData)
	require.NoError(t, err)
	call.SetLocalSDP(localSDP)
	tx, err := st.TestUA.Client.TransactionRequest(req)
	require.NoError(t, err)
	t.Cleanup(tx.Terminate)
	return call, req, tx, rtpConn
}

func enableEarlyMedia(st *serviceTest) {
	st.Server.conf.InboundEarlyMedia = &config.EarlyMediaConfig{Tone: "jp"}
}

// TestInboundEarlyMediaAnswered checks that with early media enabled the caller gets 183 with SDP
// instead of 180, and that 183 and 200 OK share the To tag and the SDP.
func TestInboundEarlyMediaAnswered(t *testing.T) {
	st := NewServiceTest(t, nil)
	enableEarlyMedia(st)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	call, req, tx, _ := earlyMediaInvite(t, st)

	res := getResponseOrFailTimeout(t, ctx, tx)
	require.Equal(t, sip.StatusCode(100), res.StatusCode)

	res183 := getResponseOrFailTimeout(t, ctx, tx)
	require.Equal(t, sip.StatusCode(183), res183.StatusCode, "should receive 183 instead of 180")
	require.NotEmpty(t, res183.Body(), "183 should carry the SDP answer")
	require.NotNil(t, res183.Contact(), "183 should carry Contact")
	tag183, ok := res183.To().Params.Get("tag")
	require.True(t, ok)

	ok200 := getFinalResponseOrFail(t, ctx, tx)
	require.Equal(t, sip.StatusCode(200), ok200.StatusCode)
	tag200, ok := ok200.To().Params.Get("tag")
	require.True(t, ok)
	require.Equal(t, tag183, tag200, "183 and 200 OK must use the same To tag")
	require.Equal(t, res183.Body(), ok200.Body(), "183 and 200 OK must carry the same SDP")

	require.NoError(t, st.TestUA.Client.WriteRequest(sip.NewAckRequest(req, ok200, nil)))
	call.SetRemoteTag(LocalTag(tag200))
	bye := call.TransactionRequest(t, call.NewRequest(sip.BYE))
	require.Equal(t, sip.StatusCode(200), bye.StatusCode, "BYE should get 200 OK")
}

// TestInboundEarlyMediaRingback checks the ringback sent to the caller while the call is not answered:
// 183 is repeated instead of 180, and the RTP carries 400+415Hz for 1s followed by 2s of silence.
func TestInboundEarlyMediaRingback(t *testing.T) {
	st := NewServiceTest(t, &serviceTestConfig{
		GetRoom: newTestRoomConfig(&testRoomConfig{ringForever: true}),
	})
	enableEarlyMedia(st)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	_, _, tx, rtpConn := earlyMediaInvite(t, st)

	res := getResponseOrFailTimeout(t, ctx, tx)
	require.Equal(t, sip.StatusCode(100), res.StatusCode)
	res183 := getResponseOrFailTimeout(t, ctx, tx)
	require.Equal(t, sip.StatusCode(183), res183.StatusCode)
	answer, err := sdp.ParseAnswer(res183.Body())
	require.NoError(t, err)

	// 183 is repeated while ringing; 180 is never sent.
	repeated := getResponseOrFailTimeout(t, ctx, tx)
	require.Equal(t, sip.StatusCode(183), repeated.StatusCode, "183 should be repeated instead of 180")

	// Collect 7s of RTP: at least one complete tone and one complete silence between partial ones.
	type run struct {
		tone bool
		dur  time.Duration
	}
	var runs []run
	var tonePCM []int16
	start := time.Now()
	buf := make([]byte, 1500)
	for time.Since(start) < 7*time.Second {
		require.NoError(t, rtpConn.SetReadDeadline(time.Now().Add(time.Second)))
		n, err := rtpConn.Read(buf)
		require.NoError(t, err, "ringback RTP should keep flowing before the call is answered")
		var p rtp.Packet
		require.NoError(t, p.Unmarshal(buf[:n]))
		pcm := decodeG711(t, answer, p)
		dur := time.Duration(len(pcm)) * time.Second / 8000
		tone := rms(pcm) > 500
		if tone {
			tonePCM = append(tonePCM, pcm...)
		}
		if len(runs) > 0 && runs[len(runs)-1].tone == tone {
			runs[len(runs)-1].dur += dur
		} else {
			runs = append(runs, run{tone: tone, dur: dur})
		}
	}
	// Drop the first and last runs: they are cut by the capture window.
	require.Greater(t, len(runs), 3, "expected tone and silence to alternate: %v", runs)
	full := runs[1 : len(runs)-1]
	var sawTone, sawSilence bool
	for _, r := range full {
		if r.tone {
			sawTone = true
			require.InDelta(t, time.Second.Seconds(), r.dur.Seconds(), 0.1, "tone should last 1s: %v", runs)
		} else {
			sawSilence = true
			require.InDelta(t, (2 * time.Second).Seconds(), r.dur.Seconds(), 0.1, "silence should last 2s: %v", runs)
		}
	}
	require.True(t, sawTone && sawSilence, "expected a complete tone and silence: %v", runs)

	// The tone energy must sit at 400/415Hz, not elsewhere.
	const rate = 8000
	at400 := goertzel(tonePCM, rate, 400) + goertzel(tonePCM, rate, 415)
	at1k := goertzel(tonePCM, rate, 1000)
	atOther := goertzel(tonePCM, rate, 425)
	require.Greater(t, at400, 20*at1k, "tone should be at 400/415Hz")
	require.Greater(t, at400, 2*atOther, "tone should not be the ETSI 425Hz one")

	// Hang up while ringing: the tone must stop.
	require.NoError(t, tx.Cancel())
	final := getFinalResponseOrFail(t, ctx, tx)
	require.Equal(t, sip.StatusCode(487), final.StatusCode)
	require.Eventually(t, func() bool {
		_ = rtpConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		_, err := rtpConn.Read(buf)
		return err != nil
	}, 5*time.Second, 10*time.Millisecond, "ringback should stop after CANCEL")
}

func decodeG711(t *testing.T, answer *sdp.Answer, p rtp.Packet) []int16 {
	t.Helper()
	out := make([]int16, len(p.Payload))
	switch p.PayloadType {
	case 0:
		for i, b := range p.Payload {
			out[i] = ulaw2linear(b)
		}
	case 8:
		for i, b := range p.Payload {
			out[i] = alaw2linear(b)
		}
	default:
		t.Fatalf("unexpected payload type %d (answer %v)", p.PayloadType, answer)
	}
	return out
}

func ulaw2linear(u byte) int16 {
	u = ^u
	t := (int(u&0x0f) << 3) + 0x84
	t <<= (u & 0x70) >> 4
	if u&0x80 != 0 {
		return int16(0x84 - t)
	}
	return int16(t - 0x84)
}

func alaw2linear(a byte) int16 {
	a ^= 0x55
	t := int(a&0x0f) << 4
	seg := (a & 0x70) >> 4
	switch seg {
	case 0:
		t += 8
	case 1:
		t += 0x108
	default:
		t += 0x108
		t <<= seg - 1
	}
	if a&0x80 != 0 {
		return int16(t)
	}
	return int16(-t)
}

func rms(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	var sum float64
	for _, v := range pcm {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(pcm)))
}

// goertzel returns the signal power at freq.
func goertzel(pcm []int16, rate, freq float64) float64 {
	k := 2 * math.Cos(2*math.Pi*freq/rate)
	var s1, s2 float64
	for _, v := range pcm {
		s0 := float64(v) + k*s1 - s2
		s2, s1 = s1, s0
	}
	return s1*s1 + s2*s2 - k*s1*s2
}
