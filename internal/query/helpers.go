package query

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	opentenant "github.com/TheGrimmChester/open-tenant-go"
)

// safeInterval maps a caller bucket token to a whitelisted ClickHouse INTERVAL.
func safeInterval(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "1m", "1 minute", "minute":
		return "1 MINUTE"
	case "5m", "5 minute":
		return "5 MINUTE"
	case "10m":
		return "10 MINUTE"
	case "15m", "15 minute":
		return "15 MINUTE"
	case "30m", "30 minute":
		return "30 MINUTE"
	case "6h", "6 hour":
		return "6 HOUR"
	case "1d", "1 day", "day":
		return "1 DAY"
	case "1h", "1 hour", "hour", "":
		return "1 HOUR"
	default:
		return "1 HOUR"
	}
}

func parseFlexibleTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1e12 {
			return time.UnixMilli(n).UTC(), nil
		}
		return time.Unix(n, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognised time %q", s)
}

func parseRangeDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty range")
	}
	if unit := s[len(s)-1]; unit == 'd' || unit == 'w' {
		n, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid range %q", s)
		}
		hours := n * 24
		if unit == 'w' {
			hours *= 7
		}
		return time.Duration(hours * float64(time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid range %q (use forms like 30m, 6h, 14d, 8w)", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("range %q must be positive", s)
	}
	return d, nil
}

func parseMetricRange(qs url.Values, now time.Time) (time.Time, time.Time, error) {
	to := now
	from := now.Add(-time.Hour)
	if rel := strings.TrimSpace(qs.Get("range")); rel != "" {
		d, err := parseRangeDuration(rel)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		return now.Add(-d), now, nil
	}
	if v := strings.TrimSpace(qs.Get("from")); v != "" {
		t, err := parseFlexibleTime(v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
		}
		from = t
	}
	if v := strings.TrimSpace(qs.Get("to")); v != "" {
		t, err := parseFlexibleTime(v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
		}
		to = t
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be after from")
	}
	return from, to, nil
}

// serviceMapTimeBound converts a dashboard from/to into a ClickHouse expression
// suitable for unquoted interpolation (now()/parseDateTimeBestEffort).
func serviceMapTimeBound(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	if decoded, err := url.QueryUnescape(raw); err == nil {
		raw = decoded
	}
	raw = safeTimeLiteral(raw)
	if raw == "" {
		return fallback
	}
	upper := strings.ToUpper(raw)
	if strings.HasPrefix(upper, "NOW()") || strings.HasPrefix(upper, "PARSEDATETIME") {
		return raw
	}
	clean := strings.Trim(raw, "'\"")
	if strings.ContainsAny(clean, "';\\") {
		return fallback
	}
	return fmt.Sprintf("parseDateTimeBestEffort('%s')", escapeSQL(clean))
}

func writeOrgProject(r *http.Request) (org, proj string) {
	return opentenant.FromRequest(r).WriteTenant()
}

func invalidServiceName(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unknown", "null", "undefined", "none":
		return true
	default:
		return false
	}
}

// rumDedupe collapses cumulative RUM re-flushes to one row per page view.
func rumDedupe(where string) string {
	return fmt.Sprintf(`SELECT
		session_id,
		pv,
		any(page_url) AS page_url,
		any(user_agent) AS user_agent,
		max(occurred_at) AS last_ts,
		min(occurred_at) AS first_ts,
		argMax(JSONExtractFloat(navigation_timing, 'total'), occurred_at) AS load_total,
		argMax(JSONExtractFloat(navigation_timing, 'dom'), occurred_at) AS load_dom,
		argMax(JSONExtractFloat(web_vitals, 'lcp'), occurred_at) AS v_lcp,
		argMax(JSONExtractFloat(web_vitals, 'cls'), occurred_at) AS v_cls,
		argMax(JSONExtractFloat(web_vitals, 'inp'), occurred_at) AS v_inp,
		argMax(JSONExtractFloat(web_vitals, 'fcp'), occurred_at) AS v_fcp,
		argMax(JSONExtractFloat(web_vitals, 'ttfb'), occurred_at) AS v_ttfb,
		argMax(JSONExtractFloat(web_vitals, 'fid'), occurred_at) AS v_fid,
		argMax(length(JSONExtractArrayRaw(ajax_requests)), occurred_at) AS ajax_n,
		argMax(length(JSONExtractArrayRaw(errors)), occurred_at) AS err_n,
		argMax(length(errors) > 2, occurred_at) AS has_errors,
		argMax(ajax_requests, occurred_at) AS ajax_json,
		argMax(errors, occurred_at) AS errors_json,
		argMax(resource_timing, occurred_at) AS resources_json
		FROM opa.rum_events
		%s
		GROUP BY session_id, if(page_view_id != '', page_view_id, concat(session_id, '|', toString(occurred_at))) AS pv`,
		where)
}

func cwvRating(metric string, v float64) string {
	if v <= 0 {
		return ""
	}
	var good, poor float64
	switch metric {
	case "lcp":
		good, poor = 2500, 4000
	case "inp":
		good, poor = 200, 500
	case "cls":
		good, poor = 0.1, 0.25
	case "fcp":
		good, poor = 1800, 3000
	case "ttfb":
		good, poor = 800, 1800
	case "fid":
		good, poor = 100, 300
	default:
		return ""
	}
	if v <= good {
		return "good"
	}
	if v <= poor {
		return "needs-improvement"
	}
	return "poor"
}

func tenantAnd(r *http.Request, alias string) string {
	return opentenant.FromRequest(r).ScopeAnd(alias)
}
