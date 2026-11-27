package observability

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGormLogsStartBeforeResult(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: NewGormLogger(), DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterGormQueryStart(db); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	mock.ExpectQuery("SELECT").WithArgs("private-value").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(1))
	var value int
	if err := db.Raw("SELECT ?", "private-value").Scan(&value).Error; err != nil {
		t.Fatal(err)
	}
	logs := output.String()
	start, result := strings.Index(logs, "database.query.start"), strings.Index(logs, "database.query.result")
	if start < 0 || result < start || strings.Contains(logs, "private-value") {
		t.Fatalf("unexpected logs: %s", logs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeSQL(t *testing.T) {
	for _, query := range []string{
		"SELECT * FROM users WHERE name = 'private-value' AND id = 12345",
		"SELECT * FROM users WHERE id = 1 -- token private-value",
		"SELECT $$private-value$$",
		"SELECT E'private-value\\'more'",
		"SELECT * FROM \"private-value\"",
	} {
		result := SafeSQL(query)
		if strings.Contains(result, "private-value") || strings.Contains(result, "12345") {
			t.Fatalf("sensitive data in sanitized SQL: %q", result)
		}
	}
}

func TestLGTMMetricIntegration(t *testing.T) {
	endpoint := os.Getenv("WBD_TEST_OTLP_ENDPOINT")
	metricsURL := os.Getenv("WBD_TEST_METRICS_URL")
	if endpoint == "" || metricsURL == "" {
		t.Skip("LGTM test endpoints not configured")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	runtime, err := Setup(context.Background(), "wbd-test")
	if err != nil {
		t.Fatal(err)
	}
	JobEvent(context.Background(), "integration.test", "SUCCEEDED")
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(metricsURL)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr == nil && strings.Contains(string(body), "wbd_migration_job_events_total") {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("job metric did not reach Collector's Prometheus endpoint")
}

func TestLGTMGrafanaRateIntegration(t *testing.T) {
	endpoint, grafana := os.Getenv("WBD_TEST_OTLP_ENDPOINT"), os.Getenv("WBD_TEST_GRAFANA_URL")
	if endpoint == "" || grafana == "" {
		t.Skip("LGTM and Grafana endpoints not configured")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	runtime, err := Setup(context.Background(), "wbd-rate-test")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(context.Background())
	for i := 0; i < 3; i++ {
		JobEvent(context.Background(), "job.finished", "SUCCEEDED")
		if err := runtime.metrics.ForceFlush(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
	}
	query := url.Values{"query": {`sum by (event, status) (rate(wbd_migration_job_events_total{service_name="wbd-rate-test"}[5m]))`}}
	address := strings.TrimRight(grafana, "/") + "/api/datasources/proxy/uid/prometheus/api/v1/query?" + query.Encode()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, address, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("admin", "admin")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"event":"job.finished"`) {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("Grafana rate query did not return a job event series")
}

func TestOTLPExportsLogsTracesAndMetrics(t *testing.T) {
	var mu sync.Mutex
	paths := map[string]int{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	runtime, err := Setup(context.Background(), "test-otlp")
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := otel.Tracer("test").Start(context.Background(), "test-span")
	slog.InfoContext(ctx, "test event", "event", "test.event")
	JobEvent(ctx, "job.finished", "SUCCEEDED")
	span.End()
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/v1/logs", "/v1/traces", "/v1/metrics"} {
		if paths[path] == 0 {
			t.Errorf("no OTLP request to %s; received %v", path, paths)
		}
	}
}

func TestHTTPMetricsUseRoutePattern(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	defer provider.Shutdown(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler, err := HTTP(mux)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/items/secret-id", nil))
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != "wbd.http.requests" {
				continue
			}
			for _, point := range metric.Data.(metricdata.Sum[int64]).DataPoints {
				for _, attr := range point.Attributes.ToSlice() {
					if strings.Contains(attr.Value.AsString(), "secret-id") {
						t.Fatal("metric contains request ID")
					}
					if strings.Contains(attr.Value.AsString(), "/items/{id}") {
						found = true
					}
				}
			}
		}
	}
	if response.Code != http.StatusNoContent || !found {
		t.Fatalf("status=%d route metric found=%v", response.Code, found)
	}
}
