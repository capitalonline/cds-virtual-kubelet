package root

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/pkg/errors"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/providers"
	"io"
	"net"
	"net/http"
)

// AcceptedCiphers is the list of accepted TLS ciphers, with known weak ciphers elided
// Note this list should be a moving target.
var AcceptedCiphers = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,

	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
}

func loadTLSConfig(certPath, keyPath string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, errors.Wrap(err, "error loading tls certs")
	}

	return &tls.Config{
		Certificates:             []tls.Certificate{cert},
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true,
		CipherSuites:             AcceptedCiphers,
	}, nil
}

func setupHTTPServer(ctx context.Context, p providers.Provider, cfg *apiServerConfig) (_ func(), retErr error) {
	var closers []io.Closer
	cancel := func() {
		for _, c := range closers {
			c.Close()
		}
	}
	defer func() {
		if retErr != nil {
			cancel()
		}
	}()

	if cfg.CertPath == "" || cfg.KeyPath == "" {
		log.G(ctx).
			WithField("certPath", cfg.CertPath).
			WithField("keyPath", cfg.KeyPath).
			Error("TLS certificates not provided, not setting up pod http server")
	} else {
		tlsCfg, err := loadTLSConfig(cfg.CertPath, cfg.KeyPath)
		if err != nil {
			return nil, err
		}
		l, err := tls.Listen("tcp", cfg.Addr, tlsCfg)
		if err != nil {
			return nil, errors.Wrap(err, "error setting up listener for pod http server")
		}

		mux := http.NewServeMux()

		podRoutes := api.PodHandlerConfig{
			RunInContainer:   p.RunInContainer,
			GetContainerLogs: p.GetContainerLogs,
			GetPods:          p.GetPods,
		}
		api.AttachPodRoutes(podRoutes, mux, true)

		s := &http.Server{
			Handler:   mux,
			TLSConfig: tlsCfg,
		}
		go serveHTTP(ctx, s, l, "pods")
		closers = append(closers, s)
	}

	if cfg.MetricsAddr == "" {
		log.G(ctx).Info("Pod metrics server not setup due to empty metrics address")
	} else {
		l, err := net.Listen("tcp", cfg.MetricsAddr)
		if err != nil {
			return nil, errors.Wrap(err, "could not setup listener for pod metrics http server")
		}

		mux := http.NewServeMux()

		var summaryHandlerFunc api.PodStatsSummaryHandlerFunc
		if mp, ok := p.(providers.PodMetricsProvider); ok {
			summaryHandlerFunc = mp.GetStatsSummary
		}
		podMetricsRoutes := api.PodMetricsConfig{
			GetStatsSummary: summaryHandlerFunc,
		}
		api.AttachPodMetricsRoutes(podMetricsRoutes, mux)
		s := &http.Server{
			Handler: mux,
		}
		go serveHTTP(ctx, s, l, "pod metrics")
		closers = append(closers, s)
	}

	return cancel, nil
}

func serveHTTP(ctx context.Context, s *http.Server, l net.Listener, name string) {
	if err := s.Serve(l); err != nil {
		select {
		case <-ctx.Done():
		default:
			log.G(ctx).WithError(err).Errorf("Error setting up %s http server", name)
		}
	}
	l.Close()
}

type apiServerConfig struct {
	CertPath    string
	KeyPath     string
	Addr        string
	MetricsAddr string
}

func getAPIConfig(c Opts) (*apiServerConfig, error) {
	config := apiServerConfig{
		CertPath: c.CertPath,
		KeyPath:  c.KeyPath,
	}

	config.Addr = fmt.Sprintf(":%d", c.ListenPort)
	config.MetricsAddr = c.MetricsAddr

	return &config, nil
}
