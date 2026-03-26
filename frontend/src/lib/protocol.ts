/**
 * Binary WebSocket frame protocol.
 *
 * Frame envelope:
 *   type   (1 byte)
 *   chanID (2 bytes, big-endian uint16)
 *   length (4 bytes, big-endian uint32)
 *   payload (N bytes)
 */

export const FrameType = {
  Data: 0x01,
  PTYResize: 0x02,
  Stats: 0x03,
  FileList: 0x04,
  FileListResp: 0x05,
  FileUpload: 0x06,
  Progress: 0x07,
  FileDownloadReq: 0x08,
  FileDownload: 0x09,
  OpenPTY: 0x0a,
  ClosePTY: 0x0b,
  SessionSync: 0x0c,
  Ping: 0x0d,
  Pong: 0x0e,
  OpenProxy: 0x0f,
  CloseProxy: 0x10,
  FileOp: 0x11,
  DesktopPush: 0x12,
  DesktopSave: 0x13,
} as const;

export type FrameTypeValue = (typeof FrameType)[keyof typeof FrameType];

export interface Frame {
  type: FrameTypeValue;
  chanID: number;
  payload: Uint8Array;
}

const HEADER_SIZE = 7; // 1 + 2 + 4

/**
 * Encode a Frame into binary wire format.
 * Layout: type(1) | chanID(2 BE) | length(4 BE) | payload(N)
 */
export function encodeFrame(frame: Frame): ArrayBuffer {
  const payloadLen = frame.payload.byteLength;
  const buf = new ArrayBuffer(HEADER_SIZE + payloadLen);
  const view = new DataView(buf);

  view.setUint8(0, frame.type);
  view.setUint16(1, frame.chanID, false /* big-endian */);
  view.setUint32(3, payloadLen, false /* big-endian */);

  const out = new Uint8Array(buf);
  out.set(frame.payload, HEADER_SIZE);

  return buf;
}

/**
 * Decode a Frame from a complete binary buffer.
 * Expects the full frame (header + payload).
 */
export function decodeFrame(buffer: ArrayBuffer): Frame {
  if (buffer.byteLength < HEADER_SIZE) {
    throw new Error(
      `Frame too short: expected at least ${HEADER_SIZE} bytes, got ${buffer.byteLength}`
    );
  }

  const view = new DataView(buffer);
  const type = view.getUint8(0) as FrameTypeValue;
  const chanID = view.getUint16(1, false /* big-endian */);
  const length = view.getUint32(3, false /* big-endian */);

  if (buffer.byteLength < HEADER_SIZE + length) {
    throw new Error(
      `Frame payload truncated: expected ${length} bytes, got ${buffer.byteLength - HEADER_SIZE}`
    );
  }

  const payload = new Uint8Array(buffer, HEADER_SIZE, length);

  return { type, chanID, payload };
}

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

/** Encode a JSON-serialisable object to a UTF-8 Uint8Array. */
export function encodeJSON(obj: unknown): Uint8Array {
  return textEncoder.encode(JSON.stringify(obj));
}

/** Decode a UTF-8 Uint8Array payload as JSON. */
export function decodeJSON<T>(payload: Uint8Array): T {
  return JSON.parse(textDecoder.decode(payload)) as T;
}
