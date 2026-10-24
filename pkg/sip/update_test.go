package sip

import (
	"testing"

	"github.com/livekit/sipgo/sip"
	"github.com/stretchr/testify/require"
)

func newTestUpdate(body []byte) *sip.Request {
	req := sip.NewRequest(sip.UPDATE, sip.Uri{User: "lk", Host: "10.0.0.1", Port: 5060})
	via := &sip.ViaHeader{ProtocolName: "SIP", ProtocolVersion: "2.0", Transport: "UDP", Host: "10.0.0.2", Port: 5060, Params: sip.NewParams()}
	via.Params.Add("branch", "z9hG4bK.test")
	req.AppendHeader(via)
	from := &sip.FromHeader{Address: sip.Uri{User: "carrier", Host: "10.0.0.2"}, Params: sip.NewParams()}
	from.Params.Add("tag", "remote")
	req.AppendHeader(from)
	to := &sip.ToHeader{Address: sip.Uri{User: "lk", Host: "10.0.0.1"}, Params: sip.NewParams()}
	to.Params.Add("tag", "local")
	req.AppendHeader(to)
	callID := sip.CallIDHeader("update-test")
	req.AppendHeader(&callID)
	req.AppendHeader(&sip.CSeqHeader{SeqNo: 1, MethodName: sip.UPDATE})
	if body != nil {
		req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
		req.SetBody(body)
	}
	return req
}

func TestUpdateResponse(t *testing.T) {
	contact := &sip.ContactHeader{Address: sip.Uri{Host: "10.0.0.1", Port: 5060}}

	r := updateResponse(newTestUpdate(nil), contact)
	require.Equal(t, sip.StatusOK, r.StatusCode)
	require.NotNil(t, r.Contact(), "a 2xx to UPDATE must carry the Contact (target refresh)")
	require.Equal(t, "10.0.0.1", r.Contact().Address.Host)

	r = updateResponse(newTestUpdate([]byte("v=0\r\n")), contact)
	require.Equal(t, sip.StatusNotAcceptableHere, r.StatusCode)
}
