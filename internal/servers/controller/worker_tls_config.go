package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/hashicorp/boundary/internal/cmd/base"
	"github.com/hashicorp/boundary/internal/servers"
	wrapping "github.com/hashicorp/go-kms-wrapping"
	"google.golang.org/protobuf/proto"
)

type workerAuthEntry struct {
	*base.WorkerAuthInfo
	conn net.Conn
}

// validateWorkerTls is called by the Go TLS stack with client info. It calls
// v1WorkerAuthConfig to validate the encryption and ensure the nonce has not
// been replayed, then stores connection information into the auth cache, which
// is used by the intercepting listener to log information.
func (c Controller) validateWorkerTls(hello *tls.ClientHelloInfo) (*tls.Config, error) {
	for _, p := range hello.SupportedProtos {
		switch {
		case strings.HasPrefix(p, "v1workerauth-"):
			tlsConf, workerInfo, err := c.v1WorkerAuthConfig(hello.SupportedProtos)
			if err == nil {
				// Store info that can be retrieved in the intercepting
				// listener.
				//
				// NOTE: This is not a load-or-store because we need to ensure
				// replays can't happen across controllers, hence storing in the
				// database in v1WorkerAuthConfig. Although this could serve as
				// a secondary check, a load-or-store here keeps the test from
				// ensuring that it is rejected at the database level via the
				// nonce unique constraint, which is by far the more important
				// thing to validate (although there are DB-level tests for that
				// too).
				c.workerAuthCache.Store(workerInfo.ConnectionNonce, &workerAuthEntry{
					WorkerAuthInfo: workerInfo,
				})
			}
			return tlsConf, err
		}
	}
	return nil, nil
}

// v1WorkerAuthConfig:
//
// * Reads the information from ALPN, combining across protos
// * Combines them to form the encrypted auth information
// * Validates it by decrypting against the configured KMS
// * Ensures that the nonce is unique to prevent replay attacks
// * Returns the shared TLS configuration that is used to establish the connection
func (c Controller) v1WorkerAuthConfig(protos []string) (*tls.Config, *base.WorkerAuthInfo, error) {
	var firstMatchProto string
	var encString string
	for _, p := range protos {
		if strings.HasPrefix(p, "v1workerauth-") {
			// Strip that and the number
			encString += strings.TrimPrefix(p, "v1workerauth-")[3:]
			if firstMatchProto == "" {
				firstMatchProto = p
			}
		}
	}
	if firstMatchProto == "" {
		return nil, nil, errors.New("no matching proto found")
	}
	marshaledEncInfo, err := base64.RawStdEncoding.DecodeString(encString)
	if err != nil {
		return nil, nil, err
	}
	encInfo := new(wrapping.EncryptedBlobInfo)
	if err := proto.Unmarshal(marshaledEncInfo, encInfo); err != nil {
		return nil, nil, err
	}
	marshaledInfo, err := c.conf.WorkerAuthKms.Decrypt(context.Background(), encInfo, nil)
	if err != nil {
		return nil, nil, err
	}
	info := new(base.WorkerAuthInfo)
	if err := json.Unmarshal(marshaledInfo, info); err != nil {
		return nil, nil, err
	}

	// Check for replays
	serversRepo, err := c.ServersRepoFn()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to fetch servers repo: %w", err)
	}
	if err := serversRepo.AddNonce(c.baseContext, info.ConnectionNonce, servers.NoncePurposeWorkerAuth); err != nil {
		return nil, nil, fmt.Errorf("unable to add connection nonce to database: %w", err)
	}

	rootCAs := x509.NewCertPool()
	if ok := rootCAs.AppendCertsFromPEM(info.CertPEM); !ok {
		return nil, info, errors.New("unable to add ca cert to cert pool")
	}
	tlsCert, err := tls.X509KeyPair(info.CertPEM, info.KeyPEM)
	if err != nil {
		return nil, info, err
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		ClientCAs:    rootCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{firstMatchProto},
		MinVersion:   tls.VersionTLS13,
	}

	return tlsConfig, info, nil
}
