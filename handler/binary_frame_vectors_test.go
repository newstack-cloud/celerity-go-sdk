package handler_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	_ "embed"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Conformance vectors for the Celerity Binary Message Format.
//
// The format is a published wire contract, and this SDK holds a genuine second
// implementation of it. Other SDKs hand framing to the runtime
// through their native bindings, but Go reaches the runtime over IPC and has no
// in-process binding to call. So the bytes here are only as correct as
// something holds them to be.
//
// A frame that differs by a byte still parses, as something other than what was
// meant, so nothing fails until a client reads a payload short by its own
// headers. This file is shared with the runtime, which asserts the same vectors
// against the Rust encoder in helpers/tests/binary_frame_vectors.rs. Change the
// copies together.
//
//go:embed testdata/binary-frame-vectors.json
var vectorsJSON []byte

type frameVectors struct {
	Encodes []struct {
		Name          string `json:"name"`
		Route         string `json:"route"`
		MessageID     string `json:"messageId"`
		RequireAck    bool   `json:"requireAck"`
		PayloadBase64 string `json:"payloadBase64"`
		FramedBase64  string `json:"framedBase64"`
	} `json:"encodes"`
	Refusals []struct {
		Name       string `json:"name"`
		Route      string `json:"route"`
		MessageID  string `json:"messageId"`
		RequireAck bool   `json:"requireAck"`
	} `json:"refusals"`
}

type BinaryFrameVectorsTestSuite struct {
	suite.Suite
	vectors frameVectors
}

func TestBinaryFrameVectorsTestSuite(t *testing.T) {
	suite.Run(t, new(BinaryFrameVectorsTestSuite))
}

func (s *BinaryFrameVectorsTestSuite) SetupSuite() {
	s.Require().NoError(json.Unmarshal(vectorsJSON, &s.vectors), "the shared vectors should parse")
}

func (s *BinaryFrameVectorsTestSuite) decode(encoded string) []byte {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	s.Require().NoError(err, "vector value should be base64")
	return decoded
}

func (s *BinaryFrameVectorsTestSuite) Test_the_encoder_agrees_with_the_shared_vectors() {
	s.Require().NotEmpty(s.vectors.Encodes, "the vectors should carry encode cases")

	for _, vector := range s.vectors.Encodes {
		s.Run(vector.Name, func() {
			payload := s.decode(vector.PayloadBase64)
			want := s.decode(vector.FramedBase64)

			got, err := handler.FrameBinaryMessage(
				vector.Route, vector.MessageID, vector.RequireAck, payload)

			s.Require().NoError(err, "framing should succeed")
			s.Equal(want, got,
				"the frame differs from the shared vector, so this encoder and the runtime's "+
					"no longer lay out the same bytes")
		})
	}
}

func (s *BinaryFrameVectorsTestSuite) Test_the_encoder_refuses_what_the_shared_vectors_refuse() {
	s.Require().NotEmpty(s.vectors.Refusals, "the vectors should carry refusal cases")

	for _, vector := range s.vectors.Refusals {
		s.Run(vector.Name, func() {
			_, err := handler.FrameBinaryMessage(
				vector.Route, vector.MessageID, vector.RequireAck, nil)

			s.Error(err,
				"framing should be refused, since truncating it into a frame would be read "+
					"as something other than what was meant")
		})
	}
}
