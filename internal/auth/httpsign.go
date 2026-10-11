// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"gitea.dev/tea/internal/settings"
	"github.com/42wim/httpsig"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// HTTPSign signs Gitea API requests with an SSH private key or certificate.
type HTTPSign struct {
	ssh.Signer
	Cert bool
}

// SignWithAlgorithm implements ssh.AlgorithmSigner for RSA keys under httpsig.
func (h *HTTPSign) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	if as, ok := h.Signer.(ssh.AlgorithmSigner); ok {
		return as.SignWithAlgorithm(rand, data, algorithm)
	}
	return h.Sign(rand, data)
}

// SignRequest attaches Signature and Digest headers required by Gitea HTTPSign.
func (h *HTTPSign) SignRequest(r *http.Request, contents []byte) error {
	headersToSign := []string{httpsig.RequestTarget, "(created)", "(expires)"}
	if h.Cert {
		pubkey, err := ssh.ParsePublicKey(h.PublicKey().Marshal())
		if err != nil {
			return errors.New("cannot parse SSH certificate for request signing")
		}
		cert, ok := pubkey.(*ssh.Certificate)
		if !ok {
			return errors.New("no SSH certificate found for request signing")
		}
		r.Header.Set("x-ssh-certificate", base64.RawStdEncoding.EncodeToString(cert.Marshal()))
		headersToSign = append(headersToSign, "x-ssh-certificate")
	}
	if r.Body != nil && r.Body != http.NoBody {
		headersToSign = append(headersToSign, "Digest")
	}
	signer, _, err := httpsig.NewSSHSigner(h, httpsig.DigestSha512, headersToSign, httpsig.Signature, 10)
	if err != nil {
		return fmt.Errorf("cannot create HTTP signature: %w", err)
	}
	keyID := "gitea"
	if !h.Cert {
		keyID = ssh.FingerprintSHA256(h.PublicKey())
	}
	return signer.SignRequest(keyID, r, contents)
}

// SignerForAccount builds an HTTP signer from persisted account SSH metadata. passphrase is
// called only for an encrypted key file; when it is nil, such a key fails with advice to use ssh-agent.
func SignerForAccount(account settings.Account, passphrase func() ([]byte, error)) (*HTTPSign, error) {
	keyPath := account.SSHKey
	if account.SSHAgent {
		keyPath = ""
	}
	if account.SSHCertPrincipal != "" || account.AuthMethod == settings.AuthSSH {
		return newHTTPSign(keyPath, true, account.SSHCertPrincipal, passphrase)
	}
	return newHTTPSign(keyPath, false, account.SSHKeyFingerprint, passphrase)
}

// newHTTPSign loads a key file, or selects an ssh-agent key matching the fingerprint or, for
// certificates, the principal.
func newHTTPSign(keyPath string, cert bool, match string, passphrase func() ([]byte, error)) (*HTTPSign, error) {
	if keyPath == "" {
		agentClient, release, err := getAgent()
		if err != nil {
			return nil, err
		}
		signer, err := agentSigner(agentClient, cert, match)
		if err != nil {
			release()
			return nil, err
		}
		// The agent connection stays open for the signer's lifetime: every request is signed by the agent.
		return signer, nil
	}
	signer, err := parsePrivateKey(keyPath, passphrase)
	if err != nil {
		return nil, err
	}
	if cert {
		certbytes, err := os.ReadFile(keyPath + "-cert.pub")
		if err != nil {
			return nil, fmt.Errorf("cannot read SSH certificate: %w", err)
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey(certbytes)
		if err != nil {
			return nil, fmt.Errorf("cannot parse SSH certificate: %w", err)
		}
		certificate, ok := pub.(*ssh.Certificate)
		if !ok {
			return nil, errors.New("SSH certificate file does not contain a certificate")
		}
		signer, err = ssh.NewCertSigner(certificate, signer)
		if err != nil {
			return nil, fmt.Errorf("cannot use SSH certificate: %w", err)
		}
	}
	return &HTTPSign{Signer: signer, Cert: cert}, nil
}

// agentSigner selects the agent key matching the fingerprint or, for certificates, the principal.
func agentSigner(agentClient agent.Agent, cert bool, match string) (*HTTPSign, error) {
	signers, err := agentClient.Signers()
	if err != nil {
		return nil, errors.New("cannot list keys from ssh-agent")
	}
	if len(signers) == 0 {
		return nil, errors.New("no signers found in ssh-agent")
	}
	if cert {
		if signer := findCertSigner(signers, match); signer != nil {
			return &HTTPSign{Signer: signer, Cert: true}, nil
		}
		return nil, fmt.Errorf("no certificate found in ssh-agent for principal %q", match)
	}
	if signer := findPubkeySigner(signers, match); signer != nil {
		return &HTTPSign{Signer: signer}, nil
	}
	return nil, fmt.Errorf("no public key found in ssh-agent for %q", match)
}

func parsePrivateKey(keyPath string, passphrase func() ([]byte, error)) (ssh.Signer, error) {
	priv, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read SSH key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(priv)
	if _, ok := errors.AsType[*ssh.PassphraseMissingError](err); ok {
		if passphrase == nil {
			return nil, fmt.Errorf("SSH key %s is passphrase protected; add it to ssh-agent with ssh-add and log in with --ssh-agent-key or --ssh-agent-principal", keyPath)
		}
		secret, promptErr := passphrase()
		if promptErr != nil {
			return nil, promptErr
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(priv, secret)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot parse SSH key: %w", err)
	}
	return signer, nil
}

func findCertSigner(sshsigners []ssh.Signer, principal string) ssh.Signer {
	for _, s := range sshsigners {
		if !strings.Contains(s.PublicKey().Type(), "cert-v01@openssh.com") {
			continue
		}
		mpubkey, err := ssh.ParsePublicKey(s.PublicKey().Marshal())
		if err != nil {
			continue
		}
		cert, ok := mpubkey.(*ssh.Certificate)
		if !ok {
			continue
		}
		if cert.ValidBefore != ssh.CertTimeInfinity && time.Until(time.Unix(int64(cert.ValidBefore), 0)) <= 10*time.Second {
			continue
		}
		if principal == "" {
			return s
		}
		if slices.Contains(cert.ValidPrincipals, principal) {
			return s
		}
	}
	return nil
}

func findPubkeySigner(sshsigners []ssh.Signer, fingerprint string) ssh.Signer {
	for _, s := range sshsigners {
		if strings.Contains(s.PublicKey().Type(), "cert-v01@openssh.com") {
			continue
		}
		if fingerprint == "" {
			return s
		}
		if strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey()))) == fingerprint {
			return s
		}
		if ssh.FingerprintSHA256(s.PublicKey()) == fingerprint {
			return s
		}
	}
	return nil
}
