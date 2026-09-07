package diag

import (
	"bufio"
	"math"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func RawGoroutinesDump() string {
	buf := make([]byte, 1024*1024)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return string(buf[:n])
		}
		if len(buf) >= 64*1024*1024 {
			return string(buf[:n])
		}
		buf = make([]byte, len(buf)*2)
	}
}

func DumpAndClusterGoroutines() (*GoroutineClusterResult, error) {
	raw := RawGoroutinesDump()
	goroutines := parseGoroutines(raw)

	total := len(goroutines)
	if total == 0 {
		return &GoroutineClusterResult{
			TotalGoroutines: 0,
			TotalGroups:     0,
			Timestamp:       time.Now(),
			Groups:          []GoroutineGroup{},
		}, nil
	}

	type groupKey struct {
		state      string
		stackTrace string
	}

	groupMap := make(map[groupKey]*GoroutineGroup)

	for _, g := range goroutines {
		var sigBuilder strings.Builder
		for _, f := range g.Frames {
			sigBuilder.WriteString(f.Function)
			sigBuilder.WriteString("\n")
		}
		key := groupKey{
			state:      g.State,
			stackTrace: sigBuilder.String(),
		}

		if grp, exists := groupMap[key]; exists {
			grp.Count++
		} else {
			var topFrame GoroutineStackFrame
			if len(g.Frames) > 0 {
				topFrame = g.Frames[0]
			}
			groupMap[key] = &GoroutineGroup{
				Count:    1,
				State:    g.State,
				TopFrame: topFrame,
				Frames:   g.Frames,
			}
		}
	}

	var groups []GoroutineGroup
	for _, grp := range groupMap {
		pct := (float64(grp.Count) / float64(total)) * 100.0
		grp.Percentage = math.Round(pct*100) / 100
		groups = append(groups, *grp)
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Count > groups[j].Count
	})

	return &GoroutineClusterResult{
		TotalGoroutines: total,
		TotalGroups:     len(groups),
		Timestamp:       time.Now(),
		Groups:          groups,
	}, nil
}

func parseGoroutines(dump string) []GoroutineInfo {
	var (
		res     []GoroutineInfo
		scanner = bufio.NewScanner(strings.NewReader(dump))
		cur     *GoroutineInfo
		curFunc string
	)

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")

		// Goroutine header: "goroutine 123 [chan receive, 5 minutes]:" or "goroutine 1 [running]:"
		if strings.HasPrefix(line, "goroutine ") && strings.HasSuffix(line, ":") {
			if cur != nil {
				res = append(res, *cur)
			}
			cur = &GoroutineInfo{}
			curFunc = ""

			trimmed := strings.TrimPrefix(line, "goroutine ")
			trimmed = strings.TrimSuffix(trimmed, ":")
			parts := strings.SplitN(trimmed, " ", 2)
			if len(parts) >= 1 {
				id, _ := strconv.ParseInt(parts[0], 10, 64)
				cur.ID = id
			}
			if len(parts) >= 2 {
				statePart := strings.Trim(parts[1], "[]")
				stateSubParts := strings.SplitN(statePart, ",", 2)
				cur.State = strings.TrimSpace(stateSubParts[0])
				if len(stateSubParts) > 1 {
					cur.WaitDuration = strings.TrimSpace(stateSubParts[1])
				}
			}
			continue
		}

		if cur == nil {
			continue
		}

		if line == "" {
			continue
		}

		// Frame line: either function line or source code location line (indented with tab or spaces)
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ") {
			locLine := strings.TrimSpace(line)
			// e.g. "/usr/local/go/src/net/http/server.go:3086 +0x5a4"
			file, lineNum := parseFileLine(locLine)
			if curFunc != "" {
				cur.Frames = append(cur.Frames, GoroutineStackFrame{
					Function: curFunc,
					File:     file,
					Line:     lineNum,
				})
				curFunc = ""
			}
		} else if strings.HasPrefix(line, "created by ") {
			// e.g. "created by net/http.(*Server).Serve in goroutine 1"
			curFunc = strings.TrimSpace(line)
		} else {
			// e.g. "net/http.(*Server).Serve(0xc00010c000, {0x10523e428, 0xc000114000})"
			// strip argument values for clean grouping
			curFunc = cleanFunctionName(strings.TrimSpace(line))
		}
	}

	if cur != nil {
		res = append(res, *cur)
	}

	return res
}

func cleanFunctionName(fn string) string {
	if idx := strings.Index(fn, "("); idx != -1 {
		return fn[:idx]
	}
	return fn
}

func parseFileLine(loc string) (string, int) {
	// e.g. "/path/to/file.go:123 +0x5a4"
	parts := strings.Split(loc, " ")
	filePathWithLine := parts[0]
	lastColon := strings.LastIndex(filePathWithLine, ":")
	if lastColon == -1 {
		return filePathWithLine, 0
	}
	file := filePathWithLine[:lastColon]
	lineStr := filePathWithLine[lastColon+1:]
	lineNum, _ := strconv.Atoi(lineStr)
	return file, lineNum
}
