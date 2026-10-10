package application

import (
	"crypto/ecdh"
	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow/proto/waCert"
	"go.mau.fi/whatsmeow/util/keys"
	"google.golang.org/protobuf/proto"
)

func noiseAgreement(private, public [32]byte) ([]byte, error) {
	key, e := ecdh.X25519().NewPrivateKey(private[:])
	if e != nil {
		return nil, e
	}
	pub, e := ecdh.X25519().NewPublicKey(public[:])
	if e != nil {
		return nil, e
	}
	return key.ECDH(pub)
}
func actualSignedNoiseChain(root, intermediate, static *keys.KeyPair) []byte {
	details, _ := proto.Marshal(&waCert.CertChain_NoiseCertificate_Details{Serial: proto.Uint32(1), IssuerSerial: proto.Uint32(0), Key: intermediate.Pub[:]})
	sig := ecc.CalculateSignature(ecc.NewDjbECPrivateKey(*root.Priv), details)
	leafDetails, _ := proto.Marshal(&waCert.CertChain_NoiseCertificate_Details{Serial: proto.Uint32(2), IssuerSerial: proto.Uint32(1), Key: static.Pub[:]})
	leafSig := ecc.CalculateSignature(ecc.NewDjbECPrivateKey(*intermediate.Priv), leafDetails)
	cert, _ := proto.Marshal(&waCert.CertChain{Intermediate: &waCert.CertChain_NoiseCertificate{Details: details, Signature: sig[:]}, Leaf: &waCert.CertChain_NoiseCertificate{Details: leafDetails, Signature: leafSig[:]}})
	return cert
}
