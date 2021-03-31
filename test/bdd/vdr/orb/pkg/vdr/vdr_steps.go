/*
Copyright SecureKey Technologies Inc. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

// Package vdr implements vdr steps
//
package vdr

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"
	ariesdid "github.com/hyperledger/aries-framework-go/pkg/doc/did"
	"github.com/hyperledger/aries-framework-go/pkg/doc/jose"
	vdrapi "github.com/hyperledger/aries-framework-go/pkg/framework/aries/api/vdr"
	"github.com/hyperledger/aries-framework-go/pkg/kms"

	"github.com/hyperledger/aries-framework-go-ext/component/vdr/orb"
	"github.com/hyperledger/aries-framework-go-ext/component/vdr/sidetree/doc"
	"github.com/hyperledger/aries-framework-go-ext/test/bdd/vdr/orb/pkg/context"
)

const (
	maxRetry   = 10
	serviceID  = "service"
	service2ID = "service2"
	// P256KeyType EC P-256 key type.
	P256KeyType       = "P256"
	p384KeyType       = "P384"
	bls12381G2KeyType = "Bls12381G2"
	// Ed25519KeyType ed25519 key type.
	Ed25519KeyType = "Ed25519"
	origin         = "origin.com"
	jsonWebKey2020 = "JsonWebKey2020"
)

// Steps is steps for VC BDD tests.
type Steps struct {
	bddContext   *context.BDDContext
	createdDID   string
	kid          string
	httpClient   *http.Client
	vdr          *orb.VDR
	keyRetriever *keyRetriever
}

// NewSteps returns new agent from client SDK.
func NewSteps(ctx *context.BDDContext) *Steps {
	keyRetriever := &keyRetriever{}

	vdr, err := orb.New(keyRetriever, orb.WithTLSConfig(ctx.TLSConfig))
	if err != nil {
		panic(err.Error())
	}

	return &Steps{bddContext: ctx, httpClient: &http.Client{}, vdr: vdr, keyRetriever: keyRetriever}
}

// RegisterSteps registers agent steps.
func (e *Steps) RegisterSteps(s *godog.Suite) {
	s.Step(`^Orb DID is created through "([^"]*)" with key type "([^"]*)" with signature suite "([^"]*)"$`,
		e.createDID)
	s.Step(`^Resolve created DID through "([^"]*)" and validate key type "([^"]*)", signature suite "([^"]*)"$`,
		e.resolveCreatedDID)
	s.Step(`^Resolve updated DID through "([^"]*)"$`,
		e.resolveUpdatedDID)
	s.Step(`^Resolve recovered DID through "([^"]*)"$`,
		e.resolveRecoveredDID)
	s.Step(`^Resolve deactivated DID through "([^"]*)"$`,
		e.resolveDeactivatedDID)
	s.Step(`^Orb DID is updated through "([^"]*)" with key type "([^"]*)" with signature suite "([^"]*)"$`,
		e.updateDID)
	s.Step(`^Orb DID is recovered through "([^"]*)" with key type "([^"]*)" with signature suite "([^"]*)"$`,
		e.recoverDID)
	s.Step(`^Orb DID is deactivated through "([^"]*)"$`,
		e.deactivateDID)
}

func (e *Steps) deactivateDID(url string) error {
	return e.vdr.Deactivate(e.createdDID, vdrapi.WithOption(orb.EndpointsOpt, []string{url}))
}

func (e *Steps) createVerificationMethod(keyType string, pubKey []byte, kid,
	signatureSuite string) (*ariesdid.VerificationMethod, error) {
	var jwk *jose.JWK

	var err error

	switch keyType {
	case P256KeyType:
		x, y := elliptic.Unmarshal(elliptic.P256(), pubKey)

		jwk, err = jose.JWKFromPublicKey(&ecdsa.PublicKey{X: x, Y: y, Curve: elliptic.P256()})
		if err != nil {
			return nil, err
		}
	case p384KeyType:
		x, y := elliptic.Unmarshal(elliptic.P384(), pubKey)

		jwk, err = jose.JWKFromPublicKey(&ecdsa.PublicKey{X: x, Y: y, Curve: elliptic.P384()})
		if err != nil {
			return nil, err
		}
	case bls12381G2KeyType:
		jwk, err = jose.JWKFromPublicKey(pubKey)
		if err != nil {
			return nil, err
		}
	default:
		jwk, err = jose.JWKFromPublicKey(ed25519.PublicKey(pubKey))
		if err != nil {
			return nil, err
		}
	}

	return ariesdid.NewVerificationMethodFromJWK(kid, signatureSuite, "", jwk)
}

func (e *Steps) recoverDID(url, keyType, signatureSuite string) error {
	kid, pubKey, err := e.getPublicKey(keyType)
	if err != nil {
		return err
	}

	recoveryKey, recoveryKeyPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	updateKey, updateKeyPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	e.keyRetriever.nextUpdatePublicKey = updateKey
	e.keyRetriever.nextRecoveryPublicKey = recoveryKey

	didDoc := &ariesdid.Doc{ID: e.createdDID}

	vm, err := e.createVerificationMethod(keyType, pubKey, kid, signatureSuite)
	if err != nil {
		return err
	}

	didDoc.Authentication = append(didDoc.Authentication, *ariesdid.NewReferencedVerification(vm,
		ariesdid.CapabilityDelegation))

	didDoc.Service = []ariesdid.Service{{ID: serviceID, Type: "type", ServiceEndpoint: "http://www.example.com/"}}

	if err := e.vdr.Update(didDoc, vdrapi.WithOption(orb.EndpointsOpt, []string{url}),
		vdrapi.WithOption(orb.RecoverOpt, true), vdrapi.WithOption(orb.AnchorOriginOpt, origin)); err != nil {
		return err
	}

	e.keyRetriever.updateKey = updateKeyPrivateKey
	e.keyRetriever.recoverKey = recoveryKeyPrivateKey

	return nil
}

func (e *Steps) updateDID(url, keyType, signatureSuite string) error {
	kid, pubKey, err := e.getPublicKey(keyType)
	if err != nil {
		return err
	}

	updateKey, updateKeyPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	e.keyRetriever.nextUpdatePublicKey = updateKey

	vm, err := e.createVerificationMethod(keyType, pubKey, kid, signatureSuite)
	if err != nil {
		return err
	}

	didDoc := &ariesdid.Doc{ID: e.createdDID}

	didDoc.Authentication = append(didDoc.Authentication, *ariesdid.NewReferencedVerification(vm,
		ariesdid.Authentication), *ariesdid.NewReferencedVerification(vm,
		ariesdid.CapabilityInvocation))

	didDoc.Service = []ariesdid.Service{
		{
			ID:              serviceID,
			Type:            "typeUpdated",
			ServiceEndpoint: "http://www.example.com/",
		},
		{
			ID:              service2ID,
			Type:            "type",
			ServiceEndpoint: "http://www.example.com/",
		},
	}

	if err := e.vdr.Update(didDoc, vdrapi.WithOption(orb.EndpointsOpt, []string{url})); err != nil {
		return err
	}

	e.keyRetriever.updateKey = updateKeyPrivateKey

	return nil
}

func (e *Steps) createDID(url, keyType, signatureSuite string) error {
	kid, pubKey, err := e.getPublicKey(keyType)
	if err != nil {
		return err
	}

	recoveryKey, recoveryKeyPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	updateKey, updateKeyPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	vm, err := e.createVerificationMethod(keyType, pubKey, kid, signatureSuite)
	if err != nil {
		return err
	}

	didDoc := &ariesdid.Doc{}

	didDoc.Authentication = append(didDoc.Authentication, *ariesdid.NewReferencedVerification(vm,
		ariesdid.Authentication))

	didDoc.Service = []ariesdid.Service{{ID: serviceID, Type: "type", ServiceEndpoint: "http://www.example.com/"}}

	createdDocResolution, err := e.vdr.Create(didDoc, vdrapi.WithOption(orb.EndpointsOpt, []string{url}),
		vdrapi.WithOption(orb.RecoveryPublicKeyOpt, recoveryKey),
		vdrapi.WithOption(orb.UpdatePublicKeyOpt, updateKey),
		vdrapi.WithOption(orb.AnchorOriginOpt, origin))
	if err != nil {
		return err
	}

	e.keyRetriever.recoverKey = recoveryKeyPrivateKey
	e.keyRetriever.updateKey = updateKeyPrivateKey

	e.createdDID = createdDocResolution.DIDDocument.ID
	e.kid = kid

	return nil
}

func (e *Steps) resolveDID(url, did string) (*ariesdid.DocResolution, error) {
	var docResolution *ariesdid.DocResolution

	for i := 1; i <= maxRetry; i++ {
		var err error
		docResolution, err = e.vdr.Read(did, vdrapi.WithOption(orb.EndpointsOpt, []string{url}))

		if err != nil && (!strings.Contains(err.Error(), "DID does not exist") || i == maxRetry) {
			return nil, err
		}

		time.Sleep(1 * time.Second)
	}

	return docResolution, nil
}

func (e *Steps) resolveDeactivatedDID(url string) error {
	docResolution, err := e.resolveDID(url, e.createdDID)
	if err != nil {
		return err
	}

	if !docResolution.DocumentMetadata.Deactivated {
		return fmt.Errorf("did not deactivated")
	}

	return nil
}

func (e *Steps) resolveRecoveredDID(url string) error {
	docResolution, err := e.resolveDID(url, e.createdDID)
	if err != nil {
		return err
	}

	if docResolution.DIDDocument.ID != e.createdDID {
		return fmt.Errorf("resolved did %s not equal to created did %s",
			docResolution.DIDDocument.ID, e.createdDID)
	}

	if len(docResolution.DIDDocument.Service) != 1 {
		return fmt.Errorf("resolved recovered did service count is not equal to %d", 1)
	}

	if len(docResolution.DIDDocument.Authentication) != 0 {
		return fmt.Errorf("resolved recovered did authentication count is not equal to %d", 0)
	}

	if len(docResolution.DIDDocument.CapabilityInvocation) != 0 {
		return fmt.Errorf("resolved recovered did capabilityInvocation count is not equal to %d", 0)
	}

	if len(docResolution.DIDDocument.CapabilityDelegation) != 1 {
		return fmt.Errorf("resolved recovered did capabilityInvocation count is not equal to %d", 1)
	}

	return nil
}

func (e *Steps) resolveUpdatedDID(url string) error {
	docResolution, err := e.resolveDID(url, e.createdDID)
	if err != nil {
		return err
	}

	if docResolution.DIDDocument.ID != e.createdDID {
		return fmt.Errorf("resolved did %s not equal to created did %s",
			docResolution.DIDDocument.ID, e.createdDID)
	}

	if len(docResolution.DIDDocument.Service) != 2 { //nolint:gomnd
		return fmt.Errorf("resolved updated did service count is not equal to %d", 2)
	}

	if len(docResolution.DIDDocument.Authentication) != 1 {
		return fmt.Errorf("resolved updated did authentication count is not equal to %d", 1)
	}

	if len(docResolution.DIDDocument.CapabilityInvocation) != 1 {
		return fmt.Errorf("resolved updated did capabilityInvocation count is not equal to %d", 1)
	}

	return nil
}

func (e *Steps) resolveCreatedDID(url, keyType, signatureSuite string) error {
	docResolution, err := e.resolveDID(url, e.createdDID)
	if err != nil {
		return err
	}

	if docResolution.DIDDocument.ID != e.createdDID {
		return fmt.Errorf("resolved did %s not equal to created did %s",
			docResolution.DIDDocument.ID, e.createdDID)
	}

	if docResolution.DIDDocument.Service[0].ID != docResolution.DIDDocument.ID+"#"+serviceID {
		return fmt.Errorf("resolved did service ID %s not equal to %s",
			docResolution.DIDDocument.Service[0].ID, docResolution.DIDDocument.ID+"#"+serviceID)
	}

	if err := e.validatePublicKey(docResolution.DIDDocument, keyType, signatureSuite); err != nil {
		return err
	}

	return nil
}

func (e *Steps) getPublicKey(keyType string) (string, []byte, error) { //nolint:gocritic
	var kt kms.KeyType

	switch keyType {
	case Ed25519KeyType:
		kt = kms.ED25519Type
	case P256KeyType:
		kt = kms.ECDSAP256TypeIEEEP1363
	case p384KeyType:
		kt = kms.ECDSAP384TypeIEEEP1363
	case bls12381G2KeyType:
		kt = kms.BLS12381G2Type
	}

	return e.bddContext.LocalKMS.CreateAndExportPubKeyBytes(kt)
}

func (e *Steps) validatePublicKey(didDoc *ariesdid.Doc, keyType, signatureSuite string) error {
	if len(didDoc.VerificationMethod) != 1 {
		return fmt.Errorf("veification method size not equal one")
	}

	expectedJwkKeyType := ""

	switch keyType {
	case Ed25519KeyType:
		expectedJwkKeyType = "OKP"
	case P256KeyType:
		expectedJwkKeyType = "EC"
	case p384KeyType:
		expectedJwkKeyType = "EC"
	case bls12381G2KeyType:
		expectedJwkKeyType = "BLS12381G2"
	}

	if signatureSuite == jsonWebKey2020 &&
		expectedJwkKeyType != didDoc.VerificationMethod[0].JSONWebKey().Kty {
		return fmt.Errorf("jwk key type : expected=%s actual=%s", expectedJwkKeyType,
			didDoc.VerificationMethod[0].JSONWebKey().Kty)
	}

	if signatureSuite == doc.Ed25519VerificationKey2018 &&
		didDoc.VerificationMethod[0].JSONWebKey() != nil {
		return fmt.Errorf("jwk is not nil for %s", signatureSuite)
	}

	return e.verifyPublicKeyAndType(didDoc, signatureSuite)
}

func (e *Steps) verifyPublicKeyAndType(didDoc *ariesdid.Doc, signatureSuite string) error {
	if didDoc.VerificationMethod[0].ID != didDoc.ID+"#"+e.kid {
		return fmt.Errorf("resolved did public key ID %s not equal to %s",
			didDoc.VerificationMethod[0].ID, didDoc.ID+"#"+e.kid)
	}

	if didDoc.VerificationMethod[0].Type != signatureSuite {
		return fmt.Errorf("resolved did public key type %s not equal to %s",
			didDoc.VerificationMethod[0].Type, signatureSuite)
	}

	return nil
}

type keyRetriever struct {
	nextRecoveryPublicKey crypto.PublicKey
	nextUpdatePublicKey   crypto.PublicKey
	updateKey             crypto.PrivateKey
	recoverKey            crypto.PrivateKey
}

func (k *keyRetriever) GetNextRecoveryPublicKey(didID string) (crypto.PublicKey, error) {
	return k.nextRecoveryPublicKey, nil
}

func (k *keyRetriever) GetNextUpdatePublicKey(didID string) (crypto.PublicKey, error) {
	return k.nextUpdatePublicKey, nil
}

func (k *keyRetriever) GetSigningKey(didID string, ot orb.OperationType) (crypto.PrivateKey, error) {
	if ot == orb.Update {
		return k.updateKey, nil
	}

	return k.recoverKey, nil
}