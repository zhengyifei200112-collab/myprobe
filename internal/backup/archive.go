package backup

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"
)

const (
	magic             = "MYPROBE-DB-BACKUP\x01"
	chunkSize         = 1 << 20
	maximumChunkSize  = 8 << 20
	minimumPassphrase = 12
	scryptN           = 32768
	scryptR           = 8
	scryptP           = 1
)

type header struct {
	Salt        [16]byte
	NoncePrefix [8]byte
	ChunkSize   uint32
}

func Encrypt(destination io.Writer, source io.Reader, passphrase string) error {
	if len(passphrase) < minimumPassphrase {
		return fmt.Errorf("passphrase must contain at least %d characters", minimumPassphrase)
	}
	var h header
	h.ChunkSize = chunkSize
	if _, err := io.ReadFull(rand.Reader, h.Salt[:]); err != nil {
		return err
	}
	if _, err := io.ReadFull(rand.Reader, h.NoncePrefix[:]); err != nil {
		return err
	}
	aead, headerBytes, err := prepare(destination, passphrase, h, true)
	if err != nil {
		return err
	}
	buffer := make([]byte, h.ChunkSize)
	for index := uint32(0); ; index++ {
		n, readErr := io.ReadFull(source, buffer)
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if n > 0 {
			if err := writeChunk(destination, aead, headerBytes, h.NoncePrefix, index, buffer[:n]); err != nil {
				return err
			}
			if index == ^uint32(0) {
				return errors.New("backup is too large")
			}
		}
		if readErr != nil {
			endIndex := index
			if n > 0 {
				if index == ^uint32(0) {
					return errors.New("backup is too large")
				}
				endIndex++
			}
			return writeChunk(destination, aead, headerBytes, h.NoncePrefix, endIndex, nil)
		}
	}
}

func Decrypt(destination io.Writer, source io.Reader, passphrase string) error {
	reader := bufio.NewReader(source)
	h, headerBytes, err := readHeader(reader)
	if err != nil {
		return err
	}
	if h.ChunkSize == 0 || h.ChunkSize > maximumChunkSize {
		return errors.New("invalid backup chunk size")
	}
	aead, _, err := prepare(io.Discard, passphrase, h, false)
	if err != nil {
		return err
	}
	for index := uint32(0); ; index++ {
		var size uint32
		if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
			return errors.New("backup is truncated")
		}
		if size > h.ChunkSize {
			return errors.New("invalid encrypted chunk size")
		}
		ciphertext := make([]byte, int(size)+aead.Overhead())
		if _, err := io.ReadFull(reader, ciphertext); err != nil {
			return errors.New("backup is truncated")
		}
		plaintext, err := aead.Open(nil, nonce(h.NoncePrefix, index), ciphertext, aad(headerBytes, index, size))
		if err != nil {
			return errors.New("wrong passphrase or corrupted backup")
		}
		if size == 0 {
			if _, err := reader.Peek(1); !errors.Is(err, io.EOF) {
				return errors.New("backup contains trailing data")
			}
			return nil
		}
		if _, err := destination.Write(plaintext); err != nil {
			return err
		}
		if index == ^uint32(0) {
			return errors.New("backup has too many chunks")
		}
	}
}

func prepare(destination io.Writer, passphrase string, h header, write bool) (cipher.AEAD, []byte, error) {
	if passphrase == "" {
		return nil, nil, errors.New("passphrase is required")
	}
	headerBytes := encodeHeader(h)
	if write {
		if _, err := destination.Write(headerBytes); err != nil {
			return nil, nil, err
		}
	}
	key, err := scrypt.Key([]byte(passphrase), h.Salt[:], scryptN, scryptR, scryptP, 32)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	aead, err := cipher.NewGCM(block)
	return aead, headerBytes, err
}

func writeChunk(destination io.Writer, aead cipher.AEAD, headerBytes []byte, prefix [8]byte, index uint32, plaintext []byte) error {
	size := uint32(len(plaintext))
	if err := binary.Write(destination, binary.BigEndian, size); err != nil {
		return err
	}
	_, err := destination.Write(aead.Seal(nil, nonce(prefix, index), plaintext, aad(headerBytes, index, size)))
	return err
}

func encodeHeader(h header) []byte {
	result := make([]byte, 0, len(magic)+len(h.Salt)+len(h.NoncePrefix)+4)
	result = append(result, magic...)
	result = append(result, h.Salt[:]...)
	result = append(result, h.NoncePrefix[:]...)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], h.ChunkSize)
	return append(result, size[:]...)
}

func readHeader(reader io.Reader) (header, []byte, error) {
	var h header
	raw := make([]byte, len(magic)+len(h.Salt)+len(h.NoncePrefix)+4)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return h, nil, errors.New("invalid backup header")
	}
	if string(raw[:len(magic)]) != magic {
		return h, nil, errors.New("unsupported backup format")
	}
	offset := len(magic)
	copy(h.Salt[:], raw[offset:offset+len(h.Salt)])
	offset += len(h.Salt)
	copy(h.NoncePrefix[:], raw[offset:offset+len(h.NoncePrefix)])
	offset += len(h.NoncePrefix)
	h.ChunkSize = binary.BigEndian.Uint32(raw[offset:])
	return h, raw, nil
}

func nonce(prefix [8]byte, index uint32) []byte {
	value := make([]byte, 12)
	copy(value, prefix[:])
	binary.BigEndian.PutUint32(value[8:], index)
	return value
}

func aad(headerBytes []byte, index, size uint32) []byte {
	hash := sha256.Sum256(headerBytes)
	value := make([]byte, len(hash)+8)
	copy(value, hash[:])
	binary.BigEndian.PutUint32(value[len(hash):], index)
	binary.BigEndian.PutUint32(value[len(hash)+4:], size)
	return value
}
