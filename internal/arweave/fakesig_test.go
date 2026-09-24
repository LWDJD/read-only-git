package arweave

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
)

// 造一份能过 VerifySignedTx 的签名字段，供提交机制的测试用。
//
// 这些测试要测的是「拿到签名之后怎么提交」，不是签名本身；但提交前有
// 本地验签这道闸，假字段（"o"/"s" 之类）会被挡下。所以测试桩也得给真签名。
// 2048 位只为快：验签只看自洽，不限制密钥长度。
func fakeSignedSig(plainTags []Tag, reward, dataSize string) (*TxSignature, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	owner := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	lastTx := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	dataRoot := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	jsonTags := encodeTxTags(plainTags)

	pairs := make([][2][]byte, 0, len(jsonTags))
	for _, tg := range jsonTags {
		n, _ := b64Decode("name", tg.Name)
		v, _ := b64Decode("value", tg.Value)
		pairs = append(pairs, [2][]byte{n, v})
	}

	seg, err := SignatureDataSegment(2, key.N.Bytes(), nil, "0", reward,
		bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{9}, 32), pairs, dataSize)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(seg)
	sigBytes, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:],
		&rsa.PSSOptions{SaltLength: 32})
	if err != nil {
		return nil, err
	}

	return &TxSignature{
		ID:        txIDFromSignature(sigBytes),
		Owner:     owner,
		Signature: base64.RawURLEncoding.EncodeToString(sigBytes),
		Reward:    reward,
		LastTx:    lastTx,
		DataRoot:  dataRoot,
		DataSize:  dataSize,
		Tags:      jsonTags,
	}, nil
}
