package hub

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Frame type constants.
const (
	FrameData            byte = 0x01 // Raw PTY/proxy data (both directions)
	FramePTYResize       byte = 0x02 // C→S: PTY resize
	FrameStats           byte = 0x03 // S→C: Stats tick
	FrameFileList        byte = 0x04 // C→S: File list request
	FrameFileListResp    byte = 0x05 // S→C: File list response
	FrameFileUpload      byte = 0x06 // C→S: File upload chunk
	FrameProgress        byte = 0x07 // S→C: Upload/download progress
	FrameFileDownloadReq byte = 0x08 // C→S: File download request
	FrameFileDownload    byte = 0x09 // S→C: File download chunk
	FrameOpenPTY         byte = 0x0A // C→S: Open new PTY
	FrameClosePTY        byte = 0x0B // C→S: Close PTY
	FrameSessionSync     byte = 0x0C // S→C: Session sync on (re)connect
	FramePing            byte = 0x0D // C→S: Ping
	FramePong            byte = 0x0E // S→C: Pong
	FrameOpenProxy       byte = 0x0F // C→S: Open port proxy
	FrameCloseProxy      byte = 0x10 // C→S: Close port proxy
	FrameFileOp          byte = 0x11 // C→S: File operation (rename/delete/chmod)
	FrameDesktopPush     byte = 0x12 // S→C: Desktop state push
	FrameDesktopSave     byte = 0x13 // C→S: Desktop state save
)

// maxPayloadSize is the maximum allowed payload size (32MB).
const maxPayloadSize = 32 * 1024 * 1024

// headerSize is the size of the frame header: type(1) + chanID(2) + length(4).
const headerSize = 7

// Frame represents a single multiplexed message.
type Frame struct {
	Type    byte
	ChanID  uint16
	Payload []byte
}

// Encode serializes a Frame into the wire format.
// Returns a 7+len(Payload) byte slice.
func Encode(f Frame) []byte {
	buf := make([]byte, headerSize+len(f.Payload))
	buf[0] = f.Type
	binary.BigEndian.PutUint16(buf[1:3], f.ChanID)
	binary.BigEndian.PutUint32(buf[3:7], uint32(len(f.Payload)))
	copy(buf[7:], f.Payload)
	return buf
}

// Decode reads exactly one frame from r.
// Returns an error if the payload length exceeds maxPayloadSize.
func Decode(r io.Reader) (Frame, error) {
	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, fmt.Errorf("read frame header: %w", err)
	}

	frameType := header[0]
	chanID := binary.BigEndian.Uint16(header[1:3])
	payloadLen := binary.BigEndian.Uint32(header[3:7])

	if payloadLen > maxPayloadSize {
		return Frame{}, fmt.Errorf("frame payload length %d exceeds maximum %d", payloadLen, maxPayloadSize)
	}

	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, fmt.Errorf("read frame payload: %w", err)
		}
	}

	return Frame{
		Type:    frameType,
		ChanID:  chanID,
		Payload: payload,
	}, nil
}
