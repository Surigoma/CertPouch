// Package server runs the distribution API over HTTPS.
package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"time"
)

func Run(ctx context.Context, address, certFile, keyFile string, clientCAs *x509.CertPool, handler http.Handler) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if handler == nil {
		return errors.New("distribution handler is required")
	}
	srv := &http.Server{
		Addr: address, Handler: handler,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if clientCAs != nil {
		srv.TLSConfig.ClientAuth = tls.RequireAndVerifyClientCert
		srv.TLSConfig.ClientCAs = clientCAs.Clone()
	}
	result := make(chan error, 1)
	go func() {
		result <- srv.ListenAndServeTLS(certFile, keyFile)
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			return errors.Join(err, srv.Close())
		}
		err := <-result
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
