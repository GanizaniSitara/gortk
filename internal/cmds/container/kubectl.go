package container

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// formatKubectlPods compresses `kubectl/oc get pods -o json` into a one-line
// phase tally plus a capped list of problem pods. Returns ok=false when the JSON
// can't be parsed so the caller can fall back to raw stdout.
//
// Faithful port of rtk's format_kubectl_pods.
func formatKubectlPods(jsonText string) (string, bool) {
	var doc struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(jsonText), &doc); err != nil {
		return "", false
	}
	if len(doc.Items) == 0 {
		return "No pods found\n", true
	}

	var running, pending, failed int
	var restartsTotal int64
	var issues []string

	for _, raw := range doc.Items {
		var pod struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					RestartCount int64 `json:"restartCount"`
					State        struct {
						Waiting struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		}
		if err := json.Unmarshal(raw, &pod); err != nil {
			continue
		}
		ns := orDash(pod.Metadata.Namespace)
		name := orDash(pod.Metadata.Name)
		phase := pod.Status.Phase
		if phase == "" {
			phase = "Unknown"
		}

		for _, c := range pod.Status.ContainerStatuses {
			restartsTotal += c.RestartCount
		}

		switch phase {
		case "Running":
			running++
		case "Pending":
			pending++
			issues = append(issues, fmt.Sprintf("%s/%s Pending", ns, name))
		case "Failed", "Error":
			failed++
			issues = append(issues, fmt.Sprintf("%s/%s %s", ns, name, phase))
		default:
			for _, c := range pod.Status.ContainerStatuses {
				w := c.State.Waiting.Reason
				if strings.Contains(w, "CrashLoop") || strings.Contains(w, "Error") {
					failed++
					issues = append(issues, fmt.Sprintf("%s/%s %s", ns, name, w))
				}
			}
		}
	}

	var parts []string
	if running > 0 {
		parts = append(parts, strconv.Itoa(running))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", pending))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d [x]", failed))
	}
	if restartsTotal > 0 {
		parts = append(parts, fmt.Sprintf("%d restarts", restartsTotal))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d pods: %s\n", len(doc.Items), strings.Join(parts, ", "))
	if len(issues) > 0 {
		b.WriteString("[warn] Issues:\n")
		for _, issue := range take(issues, maxPodsIssues) {
			fmt.Fprintf(&b, "  %s\n", issue)
		}
		if len(issues) > maxPodsIssues {
			fmt.Fprintf(&b, "  … +%d more", len(issues)-maxPodsIssues)
		}
	}
	return b.String(), true
}

// formatKubectlServices compresses `kubectl/oc get services -o json` into a
// count header plus one compact line per service (ns/name type [ports]).
//
// Faithful port of rtk's format_kubectl_services.
func formatKubectlServices(jsonText string) (string, bool) {
	var doc struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(jsonText), &doc); err != nil {
		return "", false
	}
	if len(doc.Items) == 0 {
		return "No services found\n", true
	}

	var allLines []string
	for _, raw := range doc.Items {
		var svc struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Type  string `json:"type"`
				Ports []struct {
					Port       int64           `json:"port"`
					TargetPort json.RawMessage `json:"targetPort"`
				} `json:"ports"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(raw, &svc); err != nil {
			continue
		}
		ns := orDash(svc.Metadata.Namespace)
		name := orDash(svc.Metadata.Name)
		svcType := orDash(svc.Spec.Type)

		var ports []string
		for _, p := range svc.Spec.Ports {
			port := p.Port
			target := parseTargetPort(p.TargetPort, port)
			if port == target {
				ports = append(ports, strconv.FormatInt(port, 10))
			} else {
				ports = append(ports, fmt.Sprintf("%d→%d", port, target))
			}
		}
		allLines = append(allLines, fmt.Sprintf("  %s/%s %s [%s]", ns, name, svcType, strings.Join(ports, ",")))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d services:\n", len(doc.Items))
	for _, line := range take(allLines, maxKubectlServices) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(allLines) > maxKubectlServices {
		fmt.Fprintf(&b, "  … +%d more", len(allLines)-maxKubectlServices)
		b.WriteByte('\n')
	}
	return b.String(), true
}

// parseTargetPort resolves a service targetPort that may be a JSON number or a
// numeric string, falling back to the given port when absent/unparseable.
// Mirrors rtk's as_i64().or_else(as_str().parse()).unwrap_or(port).
func parseTargetPort(raw json.RawMessage, port int64) int64 {
	if len(raw) == 0 {
		return port
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return port
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
