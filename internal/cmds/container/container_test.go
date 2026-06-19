package container

import (
	_ "embed"
	"reflect"
	"strings"
	"testing"
)

//go:embed oc_pods.json
var ocPodsFixture string

// ── formatComposePS ────────────────────────────────────

func TestFormatComposePSBasic(t *testing.T) {
	// Tab-separated --format output: Name\tImage\tStatus\tPorts
	raw := "web-1\tnginx:latest\tUp 2 hours\t0.0.0.0:80->80/tcp\n" +
		"api-1\tnode:20\tUp 2 hours\t0.0.0.0:3000->3000/tcp\n" +
		"db-1\tpostgres:16\tUp 2 hours\t0.0.0.0:5432->5432/tcp"
	out := formatComposePS(raw)
	for _, want := range []string{"3", "web", "api", "db", "Up 2 hours"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
	if len(out) >= len(raw) {
		t.Errorf("output should be shorter than raw: %d >= %d", len(out), len(raw))
	}
}

func TestFormatComposePSEmpty(t *testing.T) {
	out := formatComposePS("")
	if !strings.Contains(out, "0") {
		t.Errorf("should show zero containers: %s", out)
	}
}

func TestFormatComposePSWhitespaceOnly(t *testing.T) {
	out := formatComposePS("   \n  \n")
	if !strings.Contains(out, "0") {
		t.Errorf("should show zero containers: %s", out)
	}
}

func TestFormatComposePSExitedService(t *testing.T) {
	raw := "worker-1\tpython:3.12\tExited (1) 2 minutes ago\t"
	out := formatComposePS(raw)
	if !strings.Contains(out, "worker") {
		t.Errorf("should show service name: %s", out)
	}
	if !strings.Contains(out, "Exited") {
		t.Errorf("should show exited status: %s", out)
	}
}

func TestFormatComposePSNoPorts(t *testing.T) {
	raw := "redis-1\tredis:7\tUp 5 hours\t"
	out := formatComposePS(raw)
	if !strings.Contains(out, "redis") {
		t.Errorf("should show service name: %s", out)
	}
	var redisLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "redis") {
			redisLine = l
			break
		}
	}
	if strings.Contains(redisLine, "] [") {
		t.Errorf("should not show port brackets when empty: %s", redisLine)
	}
}

func TestFormatComposePSLongImagePath(t *testing.T) {
	raw := "app-1\tghcr.io/myorg/myapp:latest\tUp 1 hour\t0.0.0.0:8080->8080/tcp"
	out := formatComposePS(raw)
	if !strings.Contains(out, "myapp:latest") {
		t.Errorf("should shorten image to last segment: %s", out)
	}
	if strings.Contains(out, "ghcr.io") {
		t.Errorf("should not show full registry path: %s", out)
	}
}

// ── formatComposeLogs ──────────────────────────────────

func TestFormatComposeLogsBasic(t *testing.T) {
	raw := "web-1  | 192.168.1.1 - GET / 200\n" +
		"web-1  | 192.168.1.1 - GET /favicon.ico 404\n" +
		"api-1  | Server listening on port 3000\n" +
		"api-1  | Connected to database"
	out := formatComposeLogs(raw)
	if !strings.Contains(out, "Logs") {
		t.Errorf("should have compose logs header: %s", out)
	}
}

func TestFormatComposeLogsEmpty(t *testing.T) {
	out := formatComposeLogs("")
	if !strings.Contains(out, "No logs") {
		t.Errorf("should indicate no logs: %s", out)
	}
}

// ── formatComposeBuild ─────────────────────────────────

func TestFormatComposeBuildBasic(t *testing.T) {
	raw := "[+] Building 12.3s (8/8) FINISHED\n" +
		" => [web internal] load build definition from Dockerfile           0.0s\n" +
		" => [web internal] load metadata for docker.io/library/node:20     1.2s\n" +
		" => [web 1/4] FROM docker.io/library/node:20@sha256:abc123         0.0s\n" +
		" => [web 2/4] WORKDIR /app                                         0.1s\n" +
		" => [web 3/4] COPY package*.json ./                                0.1s\n" +
		" => [web 4/4] RUN npm install                                      8.5s\n" +
		" => [web] exporting to image                                       2.3s\n" +
		" => => naming to docker.io/library/myapp-web                       0.0s"
	out := formatComposeBuild(raw)
	if !strings.Contains(out, "12.3s") {
		t.Errorf("should show total build time: %s", out)
	}
	if !strings.Contains(out, "web") {
		t.Errorf("should show service name: %s", out)
	}
	if len(out) >= len(raw) {
		t.Errorf("should be shorter than raw: %d >= %d", len(out), len(raw))
	}
}

func TestFormatComposeBuildEmpty(t *testing.T) {
	out := formatComposeBuild("")
	if out == "" {
		t.Errorf("should produce output even for empty input")
	}
}

// ── compactPorts ───────────────────────────────────────

func TestCompactPortsEmpty(t *testing.T) {
	if got := compactPorts(""); got != "-" {
		t.Errorf("compactPorts(\"\") = %q, want %q", got, "-")
	}
}

func TestCompactPortsSingle(t *testing.T) {
	if got := compactPorts("0.0.0.0:8080->80/tcp"); !strings.Contains(got, "8080") {
		t.Errorf("compactPorts single = %q, want it to contain 8080", got)
	}
}

func TestCompactPortsMany(t *testing.T) {
	got := compactPorts("0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp, 0.0.0.0:8080->8080/tcp, 0.0.0.0:9090->9090/tcp")
	if !strings.Contains(got, "…") {
		t.Errorf("should truncate for >3 ports: %q", got)
	}
}

// ── k8sGetTarget ───────────────────────────────────────

func TestK8sGetTargetPodsAliases(t *testing.T) {
	for _, resource := range []string{"po", "pod", "pods"} {
		args := []string{resource, "-n", "default"}
		target, rest, ok := k8sGetTarget(args)
		if !ok || target != "pods" || !reflect.DeepEqual(rest, args[1:]) {
			t.Errorf("failed for %s: target=%q rest=%v ok=%v", resource, target, rest, ok)
		}
	}
}

func TestK8sGetTargetServicesAliases(t *testing.T) {
	for _, resource := range []string{"svc", "service", "services"} {
		args := []string{resource, "-A"}
		target, rest, ok := k8sGetTarget(args)
		if !ok || target != "services" || !reflect.DeepEqual(rest, args[1:]) {
			t.Errorf("failed for %s: target=%q rest=%v ok=%v", resource, target, rest, ok)
		}
	}
}

func TestK8sGetTargetUnsupportedResource(t *testing.T) {
	args := []string{"deployments"}
	if _, _, ok := k8sGetTarget(args); ok {
		t.Errorf("deployments should not be a compact target")
	}
}

func TestK8sGetTargetRespectsOutputFlags(t *testing.T) {
	for _, outputFlag := range []string{"-o", "-owide", "--output", "--output=json"} {
		args := []string{"pods", outputFlag, "wide"}
		if _, _, ok := k8sGetTarget(args); ok {
			t.Errorf("should pass through %s", outputFlag)
		}
	}
}

// ── oc / kubectl pods savings ──────────────────────────

func TestOcPodsSavings(t *testing.T) {
	out, ok := formatKubectlPods(ocPodsFixture)
	if !ok {
		t.Fatalf("fixture should parse")
	}
	inputTokens := len(strings.Fields(ocPodsFixture))
	outputTokens := len(strings.Fields(out))
	if inputTokens == 0 {
		t.Fatalf("empty fixture")
	}
	savings := 100.0 - (float64(outputTokens) / float64(inputTokens) * 100.0)
	if savings < 60.0 {
		t.Errorf("expected >=60%% savings, got %.1f%%", savings)
	}
}
