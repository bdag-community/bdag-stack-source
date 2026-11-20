package serialization

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
)

// ReadElements reads multiple items from r.  It is equivalent to multiple
// calls to readElement.
func ReadElements(r io.Reader, elements ...any) error {
	for _, element := range elements {
		err := readElement(r, element)
		if err != nil {
			return err
		}
	}
	return nil
}

// readElement reads the next sequence of bytes from r using little endian
// depending on the concrete type of element pointed to.
func readElement(r io.Reader, element any) error {
	// Attempt to read the element based on the concrete type via fast
	// type assertions first.
	switch e := element.(type) {
	case *uint8:
		rv, err := BinarySerializer.Uint8(r)
		if err != nil {
			return err
		}
		*e = rv
		return nil

	case *uint16:
		rv, err := BinarySerializer.Uint16(r, littleEndian)
		if err != nil {
			return err
		}
		*e = rv
		return nil

	case *int32:
		rv, err := BinarySerializer.Uint32(r, littleEndian)
		if err != nil {
			return err
		}
		*e = int32(rv)
		return nil

	case *uint32:
		rv, err := BinarySerializer.Uint32(r, littleEndian)
		if err != nil {
			return err
		}
		*e = rv
		return nil

	case *int64:
		rv, err := BinarySerializer.Uint64(r, littleEndian)
		if err != nil {
			return err
		}
		*e = int64(rv)
		return nil

	case *uint64:
		rv, err := BinarySerializer.Uint64(r, littleEndian)
		if err != nil {
			return err
		}
		*e = rv
		return nil

	case *bool:
		rv, err := BinarySerializer.Uint8(r)
		if err != nil {
			return err
		}
		if rv == 0x00 {
			*e = false
		} else {
			*e = true
		}
		return nil

	// Unix timestamp encoded as a uint32.
	// TODO fix time ambiguous
	case *Uint32Time:
		rv, err := BinarySerializer.Uint32(r, binary.LittleEndian)
		if err != nil {
			return err
		}
		*e = Uint32Time(time.Unix(int64(rv), 0))
		return nil

	// Unix timestamp encoded as an int64.
	// TODO fix time ambiguous
	case *Int64Time:
		rv, err := BinarySerializer.Uint64(r, binary.LittleEndian)
		if err != nil {
			return err
		}
		*e = Int64Time(time.Unix(int64(rv), 0))
		return nil

	// Message header checksum.
	case *[4]byte:
		_, err := io.ReadFull(r, e[:])
		if err != nil {
			return err
		}
		return nil

	case *[6]byte:
		_, err := io.ReadFull(r, e[:])
		if err != nil {
			return err
		}
		return nil

	// IP address.
	case *[16]byte:
		_, err := io.ReadFull(r, e[:])
		if err != nil {
			return err
		}
		return nil

	case *[32]byte:
		_, err := io.ReadFull(r, e[:])
		if err != nil {
			return err
		}
		return nil

	case *common.Hash:
		_, err := io.ReadFull(r, e[:])
		if err != nil {
			return err
		}
		return nil

	case *protocol.Network:
		rv, err := BinarySerializer.Uint32(r, littleEndian)
		if err != nil {
			return err
		}
		*e = protocol.Network(rv)
		return nil

	case *pow.IPow:

		// POW layout in your pipeline:
		//   [ ... header ... ][ nonce split ][ extra(169) ]
		//   nonce = last 3 bytes of header + first 1 byte of extra
		// total := int(pow.POW_LENGTH)       // 178
		// extra := int(pow.PROOFDATA_LENGTH) // 169

		// if total <= extra || extra < 1 {
		// 	return fmt.Errorf("invalid POW sizes: total=%d extra=%d", total, extra)
		// }

		// // Read the whole POW blob once
		// buf := make([]byte, total)
		// if _, err := io.ReadFull(r, buf); err != nil {
		// 	return err
		// }
		// fmt.Println("buf --------------> ", buf)
		// proofStart := total - extra // index where the 169 "extra" starts (== 9)
		// head := buf[:proofStart]    // bytes before extra
		// fmt.Println("head ====> ", head)
		// proof := buf[proofStart:] // the 169 bytes

		// if len(head) < 3 {
		// 	return fmt.Errorf("pow head too short: headLen=%d", len(head))
		// }

		// powType := pow.PowType(buf[0])

		// // --- Logs ---
		// // fmt.Printf("[DEBUG] POW_LENGTH=%d PROOFDATA_LENGTH=%d\n", pow.POW_LENGTH, pow.PROOFDATA_LENGTH)
		// // fmt.Printf("[DEBUG] headLen=%d proofLen=%d proofStart=%d\n", len(head), len(proof), proofStart)
		// // fmt.Printf("[DEBUG] head(last12)=%x\n", head[max(0, len(head)-12):])
		// // fmt.Printf("[DEBUG] proof(first16)=%x\n", proof[:min(16, len(proof))])
		// // fmt.Printf("[DEBUG] powType=%d (%s)\n", powType, pow.GetPowName(powType))

		// // Nonce is split: last 3 bytes of head + first byte of proof
		// nonceBytes := append(append([]byte{}, head[len(head)-3:]...), proof[0])
		// nonce32 := littleEndian.Uint32(nonceBytes)
		// // fmt.Printf("[DEBUG] nonceBytes(boundary)=%x nonce=0x%x\n", nonceBytes, nonce32)

		// // Construct instance (nonce is 32-bit, cast up to uint64)
		// *e = pow.GetInstance(powType, nonce32, proof)
		// return nil
		// pow 9 bytes
		// powtype 1 byte
		// nonce 8 bytes
		b := make([]byte, pow.POW_LENGTH-pow.PROOFDATA_LENGTH)
		_, err := io.ReadFull(r, b)
		if err != nil {
			return err
		}
		// powType 1 bytes + nonce 8 bytes
		powType := pow.PowType(b[0:1][0])
		if _, ok := pow.PowMapString[powType]; !ok {
			return fmt.Errorf("powType:%d don't supported!", powType)
		}
		extraNonce1 := binary.LittleEndian.Uint32(b[1:5])
		nonce := binary.LittleEndian.Uint32(b[5:9])

		leftBytes := make([]byte, pow.PROOFDATA_LENGTH)
		// different pow store different bytes
		_, err = io.ReadFull(r, leftBytes)
		if err != nil {
			return err
		}
		// set pow type 1 bytes nonce 8 bytes and proof data except types
		*e = pow.GetInstance(powType,extraNonce1, nonce, leftBytes)
		return nil

	}

	// Fall back to the slower binary.Read if a fast path was not available
	// above.
	return binary.Read(r, littleEndian, element)
}

// helpers for logs
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// WriteElements writes multiple items to w.  It is equivalent to multiple
// calls to writeElement.
func WriteElements(w io.Writer, elements ...any) error {
	for _, element := range elements {
		err := writeElement(w, element)
		if err != nil {
			return err
		}
	}
	return nil
}

// writeElement writes the little endian representation of element to w.
func writeElement(w io.Writer, element any) error {
	// Attempt to write the element based on the concrete type via fast
	// type assertions first.
	switch e := element.(type) {
	case pow.PowType:
		err := BinarySerializer.PutUint8(w, uint8(e))
		if err != nil {
			return err
		}
		return nil
	case int32:
		err := BinarySerializer.PutUint32(w, littleEndian, uint32(e))
		if err != nil {
			return err
		}
		return nil

	case uint32:
		err := BinarySerializer.PutUint32(w, littleEndian, e)
		if err != nil {
			return err
		}
		return nil

	case int64:
		err := BinarySerializer.PutUint64(w, littleEndian, uint64(e))
		if err != nil {
			return err
		}
		return nil

	case uint64:
		err := BinarySerializer.PutUint64(w, littleEndian, e)
		if err != nil {
			return err
		}
		return nil

	case bool:
		var err error
		if e {
			err = BinarySerializer.PutUint8(w, 0x01)
		} else {
			err = BinarySerializer.PutUint8(w, 0x00)
		}
		if err != nil {
			return err
		}
		return nil

	// Message header checksum.
	case [4]byte:
		_, err := w.Write(e[:])
		if err != nil {
			return err
		}
		return nil

	// IP address.
	case [16]byte:
		_, err := w.Write(e[:])
		if err != nil {
			return err
		}
		return nil

	case *common.Hash:
		_, err := w.Write(e[:])
		if err != nil {
			return err
		}
		return nil

	case protocol.Network:
		err := BinarySerializer.PutUint32(w, littleEndian, uint32(e))
		if err != nil {
			return err
		}
		return nil
		//pow bytes
	case pow.PowBytes:
		_, err := w.Write(e[:])
		if err != nil {
			return err
		}
		return nil
		//pow Result
	}
	// Fall back to the slower binary.Write if a fast path was not available
	// above.
	return binary.Write(w, littleEndian, element)
}
