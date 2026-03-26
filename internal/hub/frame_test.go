package hub

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"io"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		frame   Frame
	}{
		{
			name:  "empty payload",
			frame: Frame{Type: FrameData, ChanID: 0, Payload: nil},
		},
		{
			name:  "empty payload explicit",
			frame: Frame{Type: FramePTYResize, ChanID: 1, Payload: []byte{}},
		},
		{
			name:  "small payload",
			frame: Frame{Type: FrameStats, ChanID: 42, Payload: []byte("hello world")},
		},
		{
			name: "larger payload 4096 bytes",
			frame: func() Frame {
				data := make([]byte, 4096)
				if _, err := rand.Read(data); err != nil {
					panic(err)
				}
				return Frame{Type: FrameData, ChanID: 100, Payload: data}
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := Encode(tt.frame)
			decoded, err := Decode(bytes.NewReader(encoded))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if decoded.Type != tt.frame.Type {
				t.Errorf("Type: got %02x, want %02x", decoded.Type, tt.frame.Type)
			}
			if decoded.ChanID != tt.frame.ChanID {
				t.Errorf("ChanID: got %d, want %d", decoded.ChanID, tt.frame.ChanID)
			}
			wantPayload := tt.frame.Payload
			if len(wantPayload) == 0 {
				wantPayload = nil
			}
			if len(decoded.Payload) == 0 {
				decoded.Payload = nil
			}
			if !bytes.Equal(decoded.Payload, wantPayload) {
				t.Errorf("Payload mismatch: got %d bytes, want %d bytes", len(decoded.Payload), len(wantPayload))
			}
		})
	}
}

func TestAllFrameTypes(t *testing.T) {
	frameTypes := []byte{
		FrameData, FramePTYResize, FrameStats, FrameFileList,
		FrameFileListResp, FrameFileUpload, FrameProgress, FrameFileDownloadReq,
		FrameFileDownload, FrameOpenPTY, FrameClosePTY, FrameSessionSync,
		FramePing, FramePong, FrameOpenProxy, FrameCloseProxy,
		FrameFileOp, FrameDesktopPush, FrameDesktopSave,
	}
	for _, ft := range frameTypes {
		f := Frame{Type: ft, ChanID: 1, Payload: []byte("test")}
		encoded := Encode(f)
		decoded, err := Decode(bytes.NewReader(encoded))
		if err != nil {
			t.Errorf("frame type 0x%02x: Decode error: %v", ft, err)
			continue
		}
		if decoded.Type != ft {
			t.Errorf("frame type 0x%02x: round-trip type mismatch", ft)
		}
	}
}

func TestMultipleSequentialDecodes(t *testing.T) {
	frames := []Frame{
		{Type: FrameData, ChanID: 1, Payload: []byte("first")},
		{Type: FramePTYResize, ChanID: 2, Payload: []byte(`{"cols":80,"rows":24}`)},
		{Type: FramePing, ChanID: 0, Payload: nil},
		{Type: FrameData, ChanID: 5, Payload: make([]byte, 256)},
	}

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(Encode(f))
	}

	for i, want := range frames {
		got, err := Decode(&buf)
		if err != nil {
			t.Fatalf("frame %d: Decode error: %v", i, err)
		}
		if got.Type != want.Type {
			t.Errorf("frame %d: Type got %02x, want %02x", i, got.Type, want.Type)
		}
		if got.ChanID != want.ChanID {
			t.Errorf("frame %d: ChanID got %d, want %d", i, got.ChanID, want.ChanID)
		}
		wantPayload := want.Payload
		if len(wantPayload) == 0 {
			wantPayload = nil
		}
		if len(got.Payload) == 0 {
			got.Payload = nil
		}
		if !bytes.Equal(got.Payload, wantPayload) {
			t.Errorf("frame %d: Payload mismatch", i)
		}
	}

	// Buffer should be empty now.
	_, err := Decode(&buf)
	if err != io.EOF && err == nil {
		t.Errorf("expected EOF after all frames consumed, got: %v", err)
	}
}

func TestDecodeTruncatedHeader(t *testing.T) {
	// Only 3 bytes of header (need 7).
	_, err := Decode(bytes.NewReader([]byte{0x01, 0x00, 0x01}))
	if err == nil {
		t.Fatal("expected error for truncated header, got nil")
	}
}

func TestDecodeTruncatedPayload(t *testing.T) {
	// Build a frame that claims 10 bytes of payload but only has 5.
	header := make([]byte, 7)
	header[0] = FrameData
	header[1] = 0
	header[2] = 0
	header[3] = 0
	header[4] = 0
	header[5] = 0
	header[6] = 10 // length = 10
	data := append(header, []byte("hello")...) // only 5 bytes of payload
	_, err := Decode(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error for truncated payload, got nil")
	}
}

func TestDecodePayloadTooLarge(t *testing.T) {
	// Build a frame that claims a payload larger than maxPayloadSize.
	header := make([]byte, 7)
	header[0] = FrameData
	// Set length to 33MB > 32MB limit.
	const tooBig uint32 = 33 * 1024 * 1024
	binary.BigEndian.PutUint32(header[3:7], tooBig)
	_, err := Decode(bytes.NewReader(header))
	if err == nil {
		t.Fatal("expected error for payload exceeding max size, got nil")
	}
}

func TestEncodeDecodeLargeRandomPayload(t *testing.T) {
	data := make([]byte, 4096)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	f := Frame{Type: FrameFileUpload, ChanID: 999, Payload: data}
	encoded := Encode(f)
	if len(encoded) != headerSize+4096 {
		t.Fatalf("encoded length: got %d, want %d", len(encoded), headerSize+4096)
	}
	decoded, err := Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(decoded.Payload, data) {
		t.Errorf("payload mismatch after round-trip of 4096 random bytes")
	}
}
