package consuldp

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	envoyMetricsUrl = "http://127.0.0.1:19000/stats/prometheus"
	metricsBindAddr = "127.0.0.1:20100"
)

func (cdp *ConsulDataplane) setupMetricsServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats/prometheus", cdp.mergedMetricsHandler)
	cdp.metricsServer = &metricsServer{
		httpServer: &http.Server{
			Addr:    metricsBindAddr,
			Handler: mux,
		},
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		exitedCh: make(chan struct{}),
	}
}

func (cdp *ConsulDataplane) startMetricsServer() {
	cdp.logger.Info("starting metrics server", "address", cdp.metricsServer.httpServer.Addr)
	defer close(cdp.metricsServer.exitedCh)
	err := cdp.metricsServer.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		cdp.logger.Error("failed to serve metrics requests", "error", err)
	}
}

func (cdp *ConsulDataplane) stopMetricsServer() {
	if cdp.metricsServer != nil && cdp.metricsServer.httpServer != nil {
		cdp.logger.Debug("stopping metrics server")
		err := cdp.metricsServer.httpServer.Close()
		if err != nil {
			cdp.logger.Warn("error while closing metrics server", "error", err)
		}
	}
}

func (cdp *ConsulDataplane) metricsServerExited() <-chan struct{} {
	return cdp.metricsServer.exitedCh
}

// mergedMetricsHandler responds with merged metrics from multiple sources:
// Consul Dataplane, Envoy and (optionally) the service/application. The Envoy
// and service metrics are scraped synchronously during the handling of this
// request.
func (cdp *ConsulDataplane) mergedMetricsHandler(rw http.ResponseWriter, _ *http.Request) {
	cdp.logger.Debug("scraping Envoy metrics", "url", envoyMetricsUrl)
	if err := cdp.scrapeMetrics(rw, envoyMetricsUrl); err != nil {
		cdp.scrapeError(rw, envoyMetricsUrl, err)
		return
	}
	telem := cdp.cfg.Telemetry
	if telem == nil || telem.Prometheus.ServiceMetricsURL == "" {
		return
	}
	url := telem.Prometheus.ServiceMetricsURL
	cdp.logger.Debug("scraping service metrics", "url", url)
	if err := cdp.scrapeMetrics(rw, url); err != nil {
		cdp.scrapeError(rw, url, err)
		return
	}
}

// scrapeMetrics fetches metrics from the given url and copies them to the response.
func (cdp *ConsulDataplane) scrapeMetrics(rw http.ResponseWriter, url string) error {
	resp, err := cdp.metricsServer.client.Get(url)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("status code %d", resp.StatusCode)
	}
	// metrics are are in a text format with one per line.
	// so, when merging metrics we simply write all lines
	_, err = io.Copy(rw, resp.Body)
	return err
}

// scrapeError logs an error and responds to the http request with an error.
func (cdp *ConsulDataplane) scrapeError(rw http.ResponseWriter, url string, err error) {
	cdp.logger.Error("failed to scrape metrics", "url", url, "error", err)
	msg := fmt.Sprintf("failed to scrape metrics at url %q", url)
	http.Error(rw, msg, http.StatusInternalServerError)
}
