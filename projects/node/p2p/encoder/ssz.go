package encoder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	qtypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/prysmaticlabs/go-ssz/types"

	fastssz "github.com/ferranbt/fastssz"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prysmaticlabs/go-ssz"
)

// SSZ network encode size for data head
const (
	SszNetworkEncoderHeadSize = 16
)

// MaxGossipSize allowed for gossip messages.
// It is the maximum allowed size of uncompressed gossip messages.
var MaxGossipSize = uint64(qtypes.MaxBlockPayload + SszNetworkEncoderHeadSize) // Adjust according to block

// MaxChunkSize is the the maximum allowed size of uncompressed req/resp chunked responses.
var MaxChunkSize = uint64(qtypes.MaxBlockPayload + SszNetworkEncoderHeadSize) // Adjust according to block

// This pool defines the sync pool for our buffered snappy writers, so that they
// can be constantly reused.
var bufWriterPool = new(sync.Pool)

// This pool defines the sync pool for our buffered snappy readers, so that they
// can be constantly reused.
var bufReaderPool = new(sync.Pool)

// SszNetworkEncoder supports p2p networking encoding using SimpleSerialize
// with snappy compression (if enabled).
type SszNetworkEncoder struct {
	UseSnappyCompression bool
}

func (e SszNetworkEncoder) doEncode(msg any) ([]byte, error) {
	if chainState, ok := msg.(*pb.ChainState); ok && shouldUseLegacyChainStateSSZ(chainState.ProtocolVersion) {
		return marshalLegacyChainStateSSZ(chainState)
	}
	if v, ok := msg.(fastssz.Marshaler); ok {
		return v.MarshalSSZ()
	}
	return ssz.Marshal(msg)
}

// EncodeGossip the proto gossip message to the io.Writer.
func (e SszNetworkEncoder) EncodeGossip(w io.Writer, msg any) (int, error) {
	if msg == nil {
		return 0, nil
	}
	b, err := e.doEncode(msg)
	if err != nil {
		return 0, err
	}
	if uint64(len(b)) > MaxGossipSize {
		return 0, fmt.Errorf("gossip message exceeds max gossip size: %d bytes > %d bytes", len(b), MaxGossipSize)
	}
	if e.UseSnappyCompression {
		b = snappy.Encode(nil /*dst*/, b)
	}
	return w.Write(b)
}

// EncodeWithMaxLength the proto message to the io.Writer. This encoding prefixes the byte slice with a protobuf varint
// to indicate the size of the message. This checks that the encoded message isn't larger than the provided max limit.
func (e SszNetworkEncoder) EncodeWithMaxLength(w io.Writer, msg any) (int, error) {
	if msg == nil {
		return 0, nil
	}
	b, err := e.doEncode(msg)
	if err != nil {
		return 0, err
	}
	if uint64(len(b)) > MaxChunkSize {
		return 0, fmt.Errorf(
			"size of encoded message is %d which is larger than the provided max limit of %d",
			len(b),
			MaxChunkSize,
		)
	}
	// write varint first
	_, err = w.Write(proto.EncodeVarint(uint64(len(b))))
	if err != nil {
		return 0, err
	}
	if e.UseSnappyCompression {
		return writeSnappyBuffer(w, b)
	}
	return w.Write(b)
}

func (e SszNetworkEncoder) doDecode(b []byte, to any) error {
	if handled, err := decodeLegacyChainStateSSZ(b, to); handled {
		return err
	}
	if v, ok := to.(fastssz.Unmarshaler); ok {
		if err := v.UnmarshalSSZ(b); err != nil {
			return enrichSSZDecodeError(b, to, err)
		}
		return nil
	}
	err := ssz.Unmarshal(b, to)
	if err != nil {
		// Check if we are unmarshalling block roots
		// and then lop off the 4 byte offset and try
		// unmarshalling again. This is temporary to
		// avoid too much disruption to onyx nodes.
		// TODO(#6408)
		if _, ok := to.(*[][32]byte); ok {
			return ssz.Unmarshal(b[4:], to)
		}
		return enrichSSZDecodeError(b, to, err)
	}
	return nil
}

const (
	chainStateProtocolVersionOffset = 32
	chainStateGraphStateOffsetSlot  = 53
	chainStateUserAgentOffsetSlot   = 57
	chainStateCurrentFixedSize      = 133
	chainStateLegacyFixedSize       = 61
	chainStateEVMCheckpointOffset   = 61
)

func shouldUseLegacyChainStateSSZ(version uint32) bool {
	return version < uint32(protocol.EVMCheckpointProtocolVersion)
}

func marshalLegacyChainStateSSZ(msg *pb.ChainState) ([]byte, error) {
	if msg == nil {
		return nil, nil
	}
	if msg.GenesisHash == nil {
		msg.GenesisHash = new(pb.Hash)
	}
	if size := len(msg.GenesisHash.Hash); size != 32 {
		return nil, fastssz.ErrBytesLengthFn("common.Hash", size, 32)
	}
	if msg.GraphState == nil {
		msg.GraphState = new(pb.GraphState)
	}
	graphStateBytes, err := msg.GraphState.MarshalSSZ()
	if err != nil {
		return nil, err
	}
	if size := len(msg.UserAgent); size > 256 {
		return nil, fastssz.ErrBytesLengthFn("ChainState.UserAgent", size, 256)
	}

	graphStateOffset := chainStateLegacyFixedSize
	userAgentOffset := graphStateOffset + len(graphStateBytes)
	buf := make([]byte, 0, userAgentOffset+len(msg.UserAgent))
	buf = append(buf, msg.GenesisHash.Hash...)
	buf = appendUint32(buf, msg.ProtocolVersion)
	buf = appendUint64(buf, msg.Timestamp)
	buf = appendUint64(buf, msg.Services)
	if msg.DisableRelayTx {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	buf = appendUint32(buf, uint32(graphStateOffset))
	buf = appendUint32(buf, uint32(userAgentOffset))
	buf = append(buf, graphStateBytes...)
	buf = append(buf, msg.UserAgent...)
	return buf, nil
}

func decodeLegacyChainStateSSZ(b []byte, to any) (bool, error) {
	msg, ok := to.(*pb.ChainState)
	if !ok || !looksLikeLegacyChainStateSSZ(b) {
		return false, nil
	}
	if err := unmarshalLegacyChainStateSSZ(b, msg); err != nil {
		return true, enrichSSZDecodeError(b, to, err)
	}
	return true, nil
}

func looksLikeLegacyChainStateSSZ(b []byte) bool {
	if len(b) < chainStateLegacyFixedSize {
		return false
	}
	version := binary.LittleEndian.Uint32(b[chainStateProtocolVersionOffset : chainStateProtocolVersionOffset+4])
	if !shouldUseLegacyChainStateSSZ(version) {
		return false
	}
	graphStateOffset := binary.LittleEndian.Uint32(b[chainStateGraphStateOffsetSlot : chainStateGraphStateOffsetSlot+4])
	userAgentOffset := binary.LittleEndian.Uint32(b[chainStateUserAgentOffsetSlot : chainStateUserAgentOffsetSlot+4])
	size := uint32(len(b))
	return graphStateOffset == chainStateLegacyFixedSize &&
		userAgentOffset >= graphStateOffset &&
		userAgentOffset <= size
}

func unmarshalLegacyChainStateSSZ(b []byte, msg *pb.ChainState) error {
	if len(b) < chainStateLegacyFixedSize {
		return fastssz.ErrSize
	}
	graphStateOffset := int(binary.LittleEndian.Uint32(b[chainStateGraphStateOffsetSlot : chainStateGraphStateOffsetSlot+4]))
	userAgentOffset := int(binary.LittleEndian.Uint32(b[chainStateUserAgentOffsetSlot : chainStateUserAgentOffsetSlot+4]))
	if graphStateOffset < chainStateLegacyFixedSize || graphStateOffset > len(b) {
		return fastssz.ErrOffset
	}
	if userAgentOffset < graphStateOffset || userAgentOffset > len(b) {
		return fastssz.ErrOffset
	}
	if len(b[userAgentOffset:]) > 256 {
		return fastssz.ErrBytesLength
	}

	graphState := new(pb.GraphState)
	if err := graphState.UnmarshalSSZ(b[graphStateOffset:userAgentOffset]); err != nil {
		return fmt.Errorf("legacy chainstate graphstate: %w", err)
	}

	*msg = pb.ChainState{
		GenesisHash:     &pb.Hash{Hash: cloneBytes(b[0:32])},
		ProtocolVersion: binary.LittleEndian.Uint32(b[chainStateProtocolVersionOffset : chainStateProtocolVersionOffset+4]),
		Timestamp:       binary.LittleEndian.Uint64(b[36:44]),
		Services:        binary.LittleEndian.Uint64(b[44:52]),
		DisableRelayTx:  b[52] == 1,
		GraphState:      graphState,
		UserAgent:       cloneBytes(b[userAgentOffset:]),
	}
	return nil
}

func appendUint32(dst []byte, value uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], value)
	return append(dst, b[:]...)
}

func appendUint64(dst []byte, value uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], value)
	return append(dst, b[:]...)
}

func cloneBytes(b []byte) []byte {
	return append([]byte(nil), b...)
}

func enrichSSZDecodeError(b []byte, to any, err error) error {
	if _, ok := to.(*pb.ChainState); !ok {
		return err
	}
	return fmt.Errorf("%w (%s)", err, describeChainStateSSZPayload(b))
}

func describeChainStateSSZPayload(b []byte) string {
	fields := []string{
		fmt.Sprintf("chainstate_ssz_payload_bytes=%d", len(b)),
		fmt.Sprintf("expected_fixed_bytes=%d", chainStateCurrentFixedSize),
		fmt.Sprintf("legacy_without_evm_checkpoint_fixed_bytes=%d", chainStateLegacyFixedSize),
	}
	if len(b) >= chainStateProtocolVersionOffset+4 {
		fields = append(fields, fmt.Sprintf("partial_protocol_version=%d",
			binary.LittleEndian.Uint32(b[chainStateProtocolVersionOffset:chainStateProtocolVersionOffset+4])))
	}
	if len(b) >= chainStateGraphStateOffsetSlot+4 {
		graphStateOffset := binary.LittleEndian.Uint32(b[chainStateGraphStateOffsetSlot : chainStateGraphStateOffsetSlot+4])
		fields = append(fields, fmt.Sprintf("graphstate_offset=%d", graphStateOffset))
		if graphStateOffset < chainStateCurrentFixedSize {
			fields = append(fields, "graphstate_offset_in_fixed_section=true")
		}
		if graphStateOffset == chainStateLegacyFixedSize {
			fields = append(fields, "looks_like_legacy_chainstate_without_evm_checkpoint=true")
		}
	}
	if len(b) >= chainStateUserAgentOffsetSlot+4 {
		userAgentOffset := binary.LittleEndian.Uint32(b[chainStateUserAgentOffsetSlot : chainStateUserAgentOffsetSlot+4])
		fields = append(fields, fmt.Sprintf("useragent_offset=%d", userAgentOffset))
	}
	if len(b) >= chainStateCurrentFixedSize {
		fields = append(fields, fmt.Sprintf("evm_checkpoint_bytes_nonzero=%t",
			hasNonZeroByte(b[chainStateEVMCheckpointOffset:chainStateCurrentFixedSize])))
	}
	prefixLen := len(b)
	if prefixLen > 80 {
		prefixLen = 80
	}
	if prefixLen > 0 {
		fields = append(fields, fmt.Sprintf("prefix_hex=%x", b[:prefixLen]))
	}
	return strings.Join(fields, " ")
}

func hasNonZeroByte(b []byte) bool {
	for _, value := range b {
		if value != 0 {
			return true
		}
	}
	return false
}

// DecodeGossip decodes the bytes to the protobuf gossip message provided.
func (e SszNetworkEncoder) DecodeGossip(b []byte, to any) error {
	if e.UseSnappyCompression {
		var err error
		b, err = snappy.Decode(nil /*dst*/, b)
		if err != nil {
			return err
		}
	}
	if uint64(len(b)) > MaxGossipSize {
		return fmt.Errorf("gossip message exceeds max gossip size: %d bytes > %d bytes", len(b), MaxGossipSize)
	}
	return e.doDecode(b, to)
}

// DecodeWithMaxLength the bytes from io.Reader to the protobuf message provided.
// This checks that the decoded message isn't larger than the provided max limit.
//
// Reads exactly msgLen decompressed bytes rather than reading until io.EOF.
// This is required for a stream to carry more than one message: readVarint
// only tells us how many bytes to expect, but a stream is only guaranteed to
// hit EOF once the peer fully half-closes it (which happens at most once
// per stream). Bounding the read to msgLen means the reader's cursor lands
// exactly at the start of the next message's varint, letting a second
// DecodeWithMaxLength call on the same io.Reader succeed without the peer
// needing to close anything in between. Note msgLen is the length of the
// *uncompressed* payload (see EncodeWithMaxLength), so when snappy is
// enabled the bound is applied to the decompressed output via io.ReadFull,
// not to the raw compressed input -- limiting the raw input to msgLen bytes
// would truncate decompression, since compressed size != msgLen.
func (e SszNetworkEncoder) DecodeWithMaxLength(r io.Reader, to any) error {
	msgLen, err := readVarint(r)
	if err != nil {
		return err
	}
	if msgLen > MaxChunkSize {
		return fmt.Errorf(
			"remaining bytes %d goes over the provided max limit of %d",
			msgLen,
			MaxChunkSize,
		)
	}
	if e.UseSnappyCompression {
		sr := newBufferedReader(r)
		defer bufReaderPool.Put(sr)
		r = sr
	}
	b := make([]byte, msgLen)
	if _, err := io.ReadFull(r, b); err != nil {
		// Once the varint has been read, a bare io.EOF from io.ReadFull
		// (meaning literally zero payload bytes arrived) is not a clean
		// message-boundary end of stream -- the peer already committed to
		// sending msgLen bytes. Report it as a truncation like any other
		// short read, so callers distinguishing "no more messages" from
		// "this message was cut short" (see readVarint) aren't misled.
		if msgLen > 0 && errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return e.doDecode(b, to)
}

// ProtocolSuffix returns the appropriate suffix for protocol IDs.
func (e SszNetworkEncoder) ProtocolSuffix() string {
	if e.UseSnappyCompression {
		return "/ssz_snappy"
	}
	return "/ssz"
}

// MaxLength specifies the maximum possible length of an encoded
// chunk of data.
func (e SszNetworkEncoder) MaxLength(length int) int {
	if e.UseSnappyCompression {
		return snappy.MaxEncodedLen(length)
	}
	return length
}

// return max chunk size
func (e SszNetworkEncoder) GetMaxChunkSize() uint64 {
	return MaxChunkSize
}

func (e SszNetworkEncoder) GetSize(msg any) int {
	if v, ok := msg.(fastssz.Marshaler); ok {
		return v.SizeSSZ()
	}
	return int(types.DetermineSize(reflect.ValueOf(msg)))
}

// Writes a bytes value through a snappy buffered writer.
func writeSnappyBuffer(w io.Writer, b []byte) (int, error) {
	bufWriter := newBufferedWriter(w)
	defer bufWriterPool.Put(bufWriter)
	num, err := bufWriter.Write(b)
	if err != nil {
		return 0, err
	}
	return num, bufWriter.Close()
}

// Instantiates a new instance of the snappy buffered reader
// using our sync pool.
func newBufferedReader(r io.Reader) *snappy.Reader {
	rawReader := bufReaderPool.Get()
	if rawReader == nil {
		return snappy.NewReader(r)
	}
	bufR, ok := rawReader.(*snappy.Reader)
	if !ok {
		return snappy.NewReader(r)
	}
	bufR.Reset(r)
	return bufR
}

// Instantiates a new instance of the snappy buffered writer
// using our sync pool.
func newBufferedWriter(w io.Writer) *snappy.Writer {
	rawBufWriter := bufWriterPool.Get()
	if rawBufWriter == nil {
		return snappy.NewBufferedWriter(w)
	}
	bufW, ok := rawBufWriter.(*snappy.Writer)
	if !ok {
		return snappy.NewBufferedWriter(w)
	}
	bufW.Reset(w)
	return bufW
}
