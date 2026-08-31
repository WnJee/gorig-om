package logtool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/gin-gonic/gin"
	"github.com/jom-io/gorig/utils/errors"
	"github.com/jom-io/gorig/utils/logger"
	"github.com/rs/xid"
	"github.com/spf13/cast"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxLineSize = 1024 * 1024 // 1MB max per JSON line
const defLogDir = ".logs"

const (
	defaultSearchSize = 10
	maxSearchSize     = 50000
	maxParallelFiles  = 8
)

func getLogDir(rootDir string) string {
	if rootDir == "" {
		rootDir = "."
	}
	return filepath.Join(rootDir, defLogDir)
}

func FetchCategories(rootDir string) ([]string, *errors.Error) {
	logDir := getLogDir(rootDir)
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		return nil, errors.Verify(fmt.Sprintf("log directory does not exist: %s", logDir))
	}

	catDirList, err := os.ReadDir(logDir)
	if err != nil {
		return nil, errors.Verify(fmt.Sprintf("read log directory error: %v", err))
	}

	categories := make([]string, 0)
	for _, catDir := range catDirList {
		if catDir.IsDir() {
			isValidCategory := false
			_ = filepath.Walk(filepath.Join(logDir, catDir.Name()), func(path string, info fs.FileInfo, err error) error {
				if err != nil {
					logger.Warn(nil, "skip file", zap.Error(err))
					return nil
				}
				if !info.IsDir() && strings.HasSuffix(info.Name(), ".jsonl") {
					isValidCategory = true
					return io.EOF // Stop walking once we find a valid file
				}
				return nil
			})
			if isValidCategory {
				categories = append(categories, catDir.Name())
			}
		}
	}

	return categories, nil
}

func ListLogFiles(opts SearchOptions) (map[string]string, error) {
	if opts.RootDir == "" {
		opts.RootDir = "."
	}
	logDir := getLogDir(opts.RootDir)
	result := make(map[string]string)

	catDirList, err := os.ReadDir(logDir)
	if err != nil {
		return nil, fmt.Errorf("read log dir error: %v", err)
	}
	dirSet := make(map[string]struct{}, len(catDirList))
	for _, d := range catDirList {
		if d.IsDir() {
			dirSet[d.Name()] = struct{}{}
		}
	}
	var categories []string
	if len(opts.Categories) == 0 {
		for _, d := range catDirList {
			if d.IsDir() {
				categories = append(categories, d.Name())
			}
		}
	} else {
		for _, cat := range opts.Categories {
			if _, ok := dirSet[cat]; ok {
				categories = append(categories, cat)
			}
		}
	}

	startBound := strings.TrimSpace(opts.StartBound)
	endBound := strings.TrimSpace(opts.EndBound)
	if startBound == "" && endBound == "" {
		if strings.TrimSpace(opts.StartTime) != "" || strings.TrimSpace(opts.EndTime) != "" {
			tmp := opts
			normalizeTimeBounds(&tmp)
			if !tmp.TimeBoundsInvalid {
				startBound = tmp.StartBound
				endBound = tmp.EndBound
			}
		}
	}

	for _, cat := range categories {
		catDir := filepath.Join(logDir, cat)
		_ = filepath.WalkDir(catDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				logger.Warn(nil, "skip file", zap.Error(err))
				return nil
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".jsonl") {
				return nil
			}
			if !strings.HasPrefix(name, cat) {
				return nil
			}
			if startBound != "" || endBound != "" {
				firstTime, lastTime, ok := readLogTimeBounds(path)
				if ok {
					if endBound != "" && firstTime > endBound {
						return nil
					}
					if startBound != "" && lastTime < startBound {
						return nil
					}
				}
			}
			result[path] = name
			return nil
		})
	}

	return result, nil
}

type logBoundsEntry struct {
	first string
	last  string
	ok    bool
	size  int64
	mtime int64
}

const boundsCacheLimit = 4096

var (
	boundsMu    sync.Mutex
	boundsCache = make(map[string]logBoundsEntry)
)

func readLogTimeBounds(path string) (string, string, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", "", false
	}
	size := fi.Size()
	mtime := fi.ModTime().UnixNano()

	boundsMu.Lock()
	cached, hit := boundsCache[path]
	boundsMu.Unlock()
	if hit && cached.size == size && cached.mtime == mtime {
		return cached.first, cached.last, cached.ok
	}

	firstTime, lastTime, ok := probeLogTimeBounds(path)

	boundsMu.Lock()
	if len(boundsCache) >= boundsCacheLimit {
		boundsCache = make(map[string]logBoundsEntry)
	}
	boundsCache[path] = logBoundsEntry{
		first: firstTime,
		last:  lastTime,
		ok:    ok,
		size:  size,
		mtime: mtime,
	}
	boundsMu.Unlock()

	return firstTime, lastTime, ok
}

func probeLogTimeBounds(path string) (string, string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()

	firstTime, ok := readFirstRecordTimeString(f)
	if !ok {
		return "", "", false
	}

	lastRec, errR := readLastRecord(f)
	if errR != nil || lastRec == nil || lastRec.Record == nil {
		return "", "", false
	}
	lastTime := normalizeRecordTimeString(lastRec.Record.Time)
	if lastTime == "" {
		return "", "", false
	}

	return firstTime, lastTime, true
}

func readFirstRecordTimeString(f *os.File) (string, bool) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", false
	}

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", false
		}
		if len(line) > maxLineSize && !endsWithNewline(line) {
			skipRestOfLine(reader)
			if err == io.EOF {
				break
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			if err == io.EOF {
				break
			}
			continue
		}
		rec := parseLineToLogRecord(line)
		if rec != nil {
			t := normalizeRecordTimeString(rec.Time)
			if t != "" {
				return t, true
			}
		}
		if err == io.EOF {
			break
		}
	}

	return "", false
}

func SearchLogs(opts SearchOptions) ([]MatchedRecord, *errors.Error) {
	if opts.Size <= 0 {
		opts.Size = defaultSearchSize
	}
	if opts.Size > maxSearchSize {
		opts.Size = maxSearchSize
	}

	traceID := strings.TrimSpace(opts.TraceID)
	if traceID != "" {
		missingStart := strings.TrimSpace(opts.StartTime) == ""
		missingEnd := strings.TrimSpace(opts.EndTime) == ""
		if missingStart || missingEnd {
			return searchLogsWithTraceTimeWindow(opts)
		}
	}

	return searchLogsOnce(opts)
}

func searchLogsWithTraceTimeWindow(opts SearchOptions) ([]MatchedRecord, *errors.Error) {
	traceID := strings.TrimSpace(opts.TraceID)
	id, err := xid.FromString(traceID)
	if err != nil {
		// skip time window optimization if trace ID is not valid
		return searchLogsOnce(opts)
	}

	baseTime := id.Time().Local()

	narrowed := opts
	if strings.TrimSpace(narrowed.StartTime) == "" {
		startTime := baseTime.Add(-1 * time.Hour)
		narrowed.StartTime = startTime.Format("2006-01-02 15:04:05")
	}

	if strings.TrimSpace(narrowed.EndTime) == "" {
		endTime := baseTime.Add(1 * time.Hour)
		narrowed.EndTime = endTime.Format("2006-01-02 15:04:05")

		result, err := searchLogsOnce(narrowed)
		if err != nil || len(result) > 0 {
			return result, err
		}

		now := time.Now()
		if !now.After(endTime) {
			return result, nil
		}

		narrowed.EndTime = now.Format("2006-01-02 15:04:05")
		return searchLogsOnce(narrowed)
	}

	return searchLogsOnce(narrowed)
}

func searchLogsOnce(opts SearchOptions) ([]MatchedRecord, *errors.Error) {
	normalizeTimeBounds(&opts)
	if opts.TimeBoundsInvalid {
		return nil, nil
	}

	files, err := ListLogFiles(opts)
	if err != nil {
		return nil, errors.Verify(err.Error())
	}
	if len(files) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	startIdx := 0
	if opts.LastPath != "" {
		found := false
		for i, k := range keys {
			if k == opts.LastPath {
				startIdx = i
				found = true
				break
			}
		}
		if !found {
			return []MatchedRecord{}, nil
		}
	}
	keys = keys[startIdx:]

	levelNeedle := ""
	if opts.Level != "" {
		levelNeedle = `"level":"` + opts.Level + `"`
	}
	levelNeedles := make([]string, 0, len(opts.Levels))
	for _, l := range opts.Levels {
		if l != "" {
			levelNeedles = append(levelNeedles, `"level":"`+l+`"`)
		}
	}
	traceNeedle := ""
	if opts.TraceID != "" {
		traceNeedle = `"_trace_id_":"` + opts.TraceID + `"`
	}
	keyword := opts.Keyword
	startBound := opts.StartBound
	endBound := opts.EndBound

	if len(keys) <= 1 || opts.Size <= 100 {
		return sequentialScan(keys, opts, levelNeedle, levelNeedles, traceNeedle, keyword, startBound, endBound)
	}
	return parallelScan(keys, opts, levelNeedle, levelNeedles, traceNeedle, keyword, startBound, endBound)
}

func sequentialScan(keys []string, opts SearchOptions, levelNeedle string, levelNeedles []string, traceNeedle, keyword, startBound, endBound string) ([]MatchedRecord, *errors.Error) {
	var matchedRecords []MatchedRecord
	matchedRecords = make([]MatchedRecord, 0, opts.Size)
	for idx, filePath := range keys {
		skipLines := int64(0)
		if idx == 0 && filePath == opts.LastPath {
			skipLines = opts.LastLine
		}
		part, e := scanSingleFile(filePath, opts, levelNeedle, levelNeedles, traceNeedle, keyword, startBound, endBound, skipLines, opts.Size-len(matchedRecords))
		if e != nil {
			return nil, e
		}
		if len(part) > 0 {
			matchedRecords = append(matchedRecords, part...)
			if len(matchedRecords) >= opts.Size {
				matchedRecords = matchedRecords[:opts.Size]
				break
			}
		}
	}
	return matchedRecords, nil
}

func parallelScan(keys []string, opts SearchOptions, levelNeedle string, levelNeedles []string, traceNeedle, keyword, startBound, endBound string) ([]MatchedRecord, *errors.Error) {
	type fileResult struct {
		records []MatchedRecord
		err     *errors.Error
	}
	concurrency := runtime.GOMAXPROCS(0)
	if concurrency < 2 {
		concurrency = 2
	}
	if concurrency > maxParallelFiles {
		concurrency = maxParallelFiles
	}
	if concurrency > len(keys) {
		concurrency = len(keys)
	}
	merged := make([]MatchedRecord, 0, opts.Size)
	for batchStart := 0; batchStart < len(keys) && len(merged) < opts.Size; batchStart += concurrency {
		batchEnd := batchStart + concurrency
		if batchEnd > len(keys) {
			batchEnd = len(keys)
		}

		results := make([]fileResult, batchEnd-batchStart)
		var wg sync.WaitGroup
		for idx := batchStart; idx < batchEnd; idx++ {
			resultIdx := idx - batchStart
			filePath := keys[idx]
			skipLines := int64(0)
			if batchStart == 0 && filePath == opts.LastPath {
				skipLines = opts.LastLine
			}
			wg.Add(1)
			go func(resultIdx int, path string, skipLines int64) {
				defer wg.Done()
				part, e := scanSingleFile(path, opts, levelNeedle, levelNeedles, traceNeedle, keyword, startBound, endBound, skipLines, opts.Size)
				results[resultIdx] = fileResult{records: part, err: e}
			}(resultIdx, filePath, skipLines)
		}
		wg.Wait()

		for _, r := range results {
			need := opts.Size - len(merged)
			if need <= 0 {
				return merged, nil
			}
			if r.err != nil {
				return nil, r.err
			}
			if len(r.records) >= need {
				merged = append(merged, r.records[:need]...)
				return merged, nil
			}
			merged = append(merged, r.records...)
		}
	}
	return merged, nil
}

func scanSingleFile(filePath string, opts SearchOptions, levelNeedle string, levelNeedles []string, traceNeedle, keyword, startBound, endBound string, skipLines int64, limit int) ([]MatchedRecord, *errors.Error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, errors.Verify(fmt.Sprintf("open file error: %v", err))
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64*1024)
	var lineNumber int64
	var out []MatchedRecord
	if limit > 0 {
		out = make([]MatchedRecord, 0, limit)
	}
	for {
		line, rerr := reader.ReadString('\n')
		if len(line) == 0 && rerr != nil {
			break
		}
		lineNumber++

		if skipLines > 0 && lineNumber <= skipLines {
			if rerr == io.EOF {
				break
			}
			continue
		}

		if len(line) > maxLineSize && !endsWithNewline(line) {
			skipRestOfLine(reader)
		}

		if len(strings.TrimSpace(line)) == 0 {
			if rerr == io.EOF {
				break
			}
			continue
		}

		if levelNeedle != "" && !strings.Contains(line, levelNeedle) {
			if rerr == io.EOF {
				break
			}
			continue
		}
		if len(levelNeedles) > 0 {
			matched := false
			for _, nd := range levelNeedles {
				if strings.Contains(line, nd) {
					matched = true
					break
				}
			}
			if !matched {
				if rerr == io.EOF {
					break
				}
				continue
			}
		}
		if traceNeedle != "" && !strings.Contains(line, traceNeedle) {
			if rerr == io.EOF {
				break
			}
			continue
		}
		if keyword != "" && !strings.Contains(line, keyword) {
			if rerr == io.EOF {
				break
			}
			continue
		}

		rec := parseLineToLogRecord(line)
		if rec == nil {
			if rerr == io.EOF {
				break
			}
			continue
		}
		if !postFilterFast(rec, opts, startBound, endBound, keyword) {
			if rerr == io.EOF {
				break
			}
			continue
		}
		out = append(out, MatchedRecord{
			FilePath:   filePath,
			LineNumber: lineNumber,
			Record:     rec,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
		if rerr == io.EOF {
			break
		}
	}
	return out, nil
}

func postFilterFast(r *LogRecord, opts SearchOptions, startBound, endBound, keyword string) bool {
	if opts.TraceID != "" && r.TraceID != opts.TraceID {
		return false
	}
	if opts.TimeBoundsInvalid {
		return false
	}
	if startBound != "" || endBound != "" {
		rt := normalizeRecordTimeString(r.Time)
		if rt == "" {
			return false
		}
		if startBound != "" && rt < startBound {
			return false
		}
		if endBound != "" && rt > endBound {
			return false
		}
	}
	if keyword != "" {
		if strings.Contains(r.Msg, keyword) {
			return true
		}
		if strings.Contains(r.Error, keyword) {
			return true
		}
		if r.Data != nil {
			for _, v := range r.Data {
				if strings.Contains(v, keyword) {
					return true
				}
			}
			return false
		}
		return false
	}
	return true
}

func preFilter(line string, opts SearchOptions) bool {
	if opts.Level != "" && !strings.Contains(line, `"level":"`+opts.Level+`"`) {
		return false
	}
	if len(opts.Levels) > 0 {
		levelMatched := false
		for _, level := range opts.Levels {
			if strings.Contains(line, `"level":"`+level+`"`) {
				levelMatched = true
				break
			}
		}
		if !levelMatched {
			return false
		}
	}
	if opts.TraceID != "" && !strings.Contains(line, `"_trace_id_":"`+opts.TraceID+`"`) {
		return false
	}
	if opts.Keyword != "" && !strings.Contains(line, opts.Keyword) {
		return false
	}
	return true
}

func postFilter(r LogRecord, opts SearchOptions) bool {
	if opts.TraceID != "" && r.TraceID != opts.TraceID {
		return false
	}

	if opts.TimeBoundsInvalid {
		return false
	}

	if opts.StartBound != "" || opts.EndBound != "" {
		rt := normalizeRecordTimeString(r.Time)
		if rt == "" {
			return false
		}
		if opts.StartBound != "" && rt < opts.StartBound {
			return false
		}
		if opts.EndBound != "" && rt > opts.EndBound {
			return false
		}
	}

	if opts.Keyword != "" {
		kw := opts.Keyword
		if strings.Contains(r.Msg, kw) {
			return true
		}
		if strings.Contains(r.Error, kw) {
			return true
		}
		if r.Data != nil {
			for _, v := range r.Data {
				if strings.Contains(v, kw) {
					return true
				}
			}
			return false
		}
		return false
	}

	return true
}

// matchRecord Match a log record with search options
func matchRecord(r LogRecord, opts SearchOptions) bool {
	// 1. level
	if opts.Level != "" && !strings.EqualFold(r.Level, opts.Level) {
		return false
	}

	if len(opts.Levels) > 0 {
		levelMatched := false
		for _, level := range opts.Levels {
			if strings.EqualFold(r.Level, level) {
				levelMatched = true
				break
			}
		}
		if !levelMatched {
			return false
		}
	}

	// 2. trace ID
	if opts.TraceID != "" && r.TraceID != opts.TraceID {
		return false
	}

	// 3. Time range
	if opts.TimeBoundsInvalid {
		return false
	}
	if opts.StartBound != "" || opts.EndBound != "" {
		rt := normalizeRecordTimeString(r.Time)
		if rt == "" {
			return false
		}
		if opts.StartBound != "" && rt < opts.StartBound {
			return false
		}
		if opts.EndBound != "" && rt > opts.EndBound {
			return false
		}
	}

	// 4. Keyword
	if opts.Keyword != "" {
		kw := opts.Keyword
		if strings.Contains(r.Msg, kw) {
			return true
		}
		if strings.Contains(r.Error, kw) {
			return true
		}
		if r.Data != nil {
			for _, v := range r.Data {
				if strings.Contains(v, kw) {
					return true
				}
			}
			return false
		}
		return false
	}

	return true
}

func normalizeTimeBounds(opts *SearchOptions) {
	opts.StartBound = ""
	opts.EndBound = ""
	opts.TimeBoundsInvalid = false

	start := strings.TrimSpace(opts.StartTime)
	if start != "" {
		t, err := time.ParseInLocation("2006-01-02 15:04:05", start, time.Local)
		if err != nil {
			opts.TimeBoundsInvalid = true
			return
		}
		opts.StartBound = t.Format("2006-01-02 15:04:05.000")
	}

	end := strings.TrimSpace(opts.EndTime)
	if end != "" {
		t, err := time.ParseInLocation("2006-01-02 15:04:05", end, time.Local)
		if err != nil {
			opts.TimeBoundsInvalid = true
			return
		}
		opts.EndBound = t.Format("2006-01-02 15:04:05.000")
	}
}

func normalizeRecordTimeString(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return ""
	}
	if len(t) == 19 {
		return t + ".000"
	}
	if len(t) > 23 {
		return t[:23]
	}
	return t
}

type ContextLogLine struct {
	FilePath   string     `json:"path"`
	LineNumber int64      `json:"line"`
	Content    string     `json:"content"`
	Record     *LogRecord `json:"record"`
}

func FetchContextLines(filePath string, centerLine, contextRange int64) ([]ContextLogLine, *errors.Error) {
	if centerLine < 1 {
		return nil, errors.Verify("center line must be greater than 0")
	}
	if contextRange < 0 {
		contextRange = 0
	}

	startLine := centerLine - contextRange
	if startLine < 1 {
		startLine = 1
	}
	endLine := centerLine + contextRange

	f, err := os.Open(filePath)
	if err != nil {
		return nil, errors.Verify(fmt.Sprintf("open file error: %v", err))
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	var result []ContextLogLine
	var currentLine int64 = 0

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, errors.Verify(fmt.Sprintf("read line error: %v", err))
		}

		currentLine++
		if currentLine < startLine {
			if len(line) > maxLineSize && !endsWithNewline(line) {
				skipRestOfLine(reader)
			}
			if err == io.EOF {
				break
			}
			continue
		}
		if currentLine > endLine {
			break
		}

		if len(line) > maxLineSize {
			if !endsWithNewline(line) {
				skipRestOfLine(reader)
			}
			continue
		}

		dataMap := map[string]interface{}{}
		if err := json.Unmarshal([]byte(line), &dataMap); err != nil {
			//logger.Error(nil, "unmarshal record error", zap.Error(err))
			continue
		}
		rec := map2LogRecord(dataMap)
		result = append(result, ContextLogLine{
			FilePath:   filePath,
			LineNumber: currentLine,
			Content:    line,
			Record:     rec,
		})

		if err == io.EOF {
			break
		}
	}

	return result, nil
}

func endsWithNewline(line string) bool {
	return len(line) > 0 && line[len(line)-1] == '\n'
}

func skipRestOfLine(reader *bufio.Reader) {
	for {
		b, err := reader.ReadByte()
		if err != nil || b == '\n' {
			break
		}
	}
}

// MonitorLogs monitors logs based on categories and conditions in real-time
func MonitorLogs(ctx *gin.Context, opts SearchOptions) *errors.Error {
	normalizeTimeBounds(&opts)

	ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	ctx.Writer.Header().Set("Content-Type", "text/event-stream")
	ctx.Writer.Header().Set("Cache-Control", "no-cache")
	ctx.Writer.Header().Set("Connection", "keep-alive")
	ctx.Writer.Flush()

	// Initialize fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return errors.Verify(fmt.Sprintf("unable to create watcher: %v", err))
	}
	defer watcher.Close()

	// Get the files to monitor based on categories
	files, err := ListLogFiles(opts)
	if err != nil {
		return errors.Verify(fmt.Sprintf("unable to list log files: %v", err))
	}

	// Initialize file offsets and add files to the watcher
	fileOffsets := make(map[string]int64)
	for file := range files {
		if fi, err := os.Stat(file); err == nil {
			fileOffsets[file] = fi.Size()
		}
		err = watcher.Add(file)
		if err != nil {
			return errors.Verify(fmt.Sprintf("unable to add file to watcher: %v", err))
		}
	}

	ctx.SSEvent("message", "monitoring started")
	ctx.Writer.Flush()
	defer func() {
		ctx.SSEvent("message", "monitoring stopped")
		ctx.Writer.Flush()
	}()
	// Monitor file events and process new logs
	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				records, newOffset, errP := processFileIncremental(event.Name, fileOffsets[event.Name], opts)
				if errP == nil {
					fileOffsets[event.Name] = newOffset
					for _, result := range records {
						ctx.SSEvent("message", result)
						ctx.Writer.Flush()
					}
				}
			}
		case err := <-watcher.Errors:
			if err != nil {
				return errors.Verify(fmt.Sprintf("watcher error: %v", err))
			}
		case <-ctx.Request.Context().Done():
			return nil
		}
	}
}

// processFileIncremental processes newly appended lines in a log file from lastOffset
func processFileIncremental(filePath string, lastOffset int64, opts SearchOptions) ([]MatchedRecord, int64, *errors.Error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, lastOffset, errors.Verify(fmt.Sprintf("unable to open log file: %v", err))
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, lastOffset, errors.Verify(fmt.Sprintf("unable to stat log file: %v", err))
	}
	size := fi.Size()
	if size < lastOffset {
		// File was truncated or rotated
		lastOffset = 0
	}
	if size == lastOffset {
		return nil, lastOffset, nil
	}

	if _, err := f.Seek(lastOffset, io.SeekStart); err != nil {
		return nil, lastOffset, errors.Verify(fmt.Sprintf("unable to seek file: %v", err))
	}

	reader := bufio.NewReaderSize(f, 64*1024)
	var records []MatchedRecord
	offset := lastOffset
	for {
		line, rerr := reader.ReadString('\n')
		if len(line) == 0 && rerr != nil {
			break
		}
		offset += int64(len(line))
		if len(line) > maxLineSize && !endsWithNewline(line) {
			skipRestOfLine(reader)
		}
		if len(strings.TrimSpace(line)) == 0 {
			if rerr == io.EOF {
				break
			}
			continue
		}
		rec := parseLineToLogRecord(line)
		if rec != nil && matchRecord(*rec, opts) {
			records = append(records, MatchedRecord{
				FilePath:   filePath,
				LineNumber: -1,
				Record:     rec,
			})
		}
		if rerr == io.EOF {
			break
		}
	}
	return records, offset, nil
}

// map2LogRecord
func map2LogRecord(dataMap map[string]interface{}) *LogRecord {
	rec := &LogRecord{}
	for k, v := range dataMap {
		switch k {
		case "level":
			rec.Level = cast.ToString(v)
		case "time":
			rec.Time = cast.ToString(v)
		case "_trace_id_":
			rec.TraceID = cast.ToString(v)
		case "msg":
			rec.Msg = cast.ToString(v)
		case "error":
			rec.Error = cast.ToString(v)
		default:
			if rec.Data == nil {
				rec.Data = map[string]string{}
			}
			rec.Data[k] = cast.ToString(v)
			if rec.Data[k] == "" {
				jsonBytes, _ := json.Marshal(v)
				rec.Data[k] = string(jsonBytes)
			}
		}
	}
	return rec
}

func parseLineToLogRecord(line string) *LogRecord {
	rec := &LogRecord{
		Data: map[string]string{},
	}
	gjson.Parse(line).ForEach(func(key, value gjson.Result) bool {
		k := key.String()
		switch k {
		case "level":
			rec.Level = value.String()
		case "time":
			rec.Time = value.String()
		case "_trace_id_":
			rec.TraceID = value.String()
		case "msg":
			rec.Msg = value.String()
		case "error":
			rec.Error = value.String()
		default:
			v := value.String()
			if v == "" {
				rec.Data[k] = value.Raw
			} else {
				rec.Data[k] = v
			}
		}
		return true
	})
	return rec
}

// readLastRecord
func readLastRecord(f *os.File) (*MatchedRecord, *errors.Error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, errors.Verify(fmt.Sprintf("unable to get file info: %v", err))
	}

	if fi.Size() == 0 {
		return nil, errors.Verify("log file is empty")
	}

	const chunkSize = 4096
	buf := make([]byte, chunkSize)
	offset := int64(0)
	size := fi.Size()
	var tail []byte
	for offset < size {
		readSize := int64(chunkSize)
		if size-offset < readSize {
			readSize = size - offset
		}
		offset += readSize
		start := size - offset
		_, err := f.Seek(start, io.SeekStart)
		if err != nil {
			return nil, errors.Verify(fmt.Sprintf("unable to seek file: %v", err))
		}

		n, err := f.Read(buf[:int(readSize)])
		if err != nil && err != io.EOF {
			return nil, errors.Verify(fmt.Sprintf("unable to read file: %v", err))
		}
		if n == 0 {
			continue
		}

		tail = append(buf[:n], tail...)

		fullRead := offset >= fi.Size()
		for {
			line, rest, ok := takeLastLine(tail, fullRead)
			if !ok {
				break
			}
			tail = rest
			if len(line) > maxLineSize {
				continue
			}
			if strings.TrimSpace(string(line)) == "" {
				continue
			}
			record := parseLineToLogRecord(string(line))
			if record == nil || record.Time == "" {
				continue
			}
			result := &MatchedRecord{
				FilePath:   f.Name(),
				LineNumber: -1,
				Record:     record,
			}
			return result, nil
		}
	}

	return nil, errors.Verify("no complete record found")
}

func takeLastLine(tail []byte, fullRead bool) ([]byte, []byte, bool) {
	if len(tail) == 0 {
		return nil, nil, false
	}
	end := len(tail)
	for end > 0 && (tail[end-1] == '\n' || tail[end-1] == '\r') {
		end--
	}
	if end == 0 {
		return nil, nil, false
	}
	idx := bytes.LastIndexByte(tail[:end], '\n')
	if idx == -1 {
		if !fullRead {
			return nil, nil, false
		}
		return tail[:end], nil, true
	}
	return tail[idx+1 : end], tail[:idx+1], true
}

// DownloadLogs downloads logs based on categories and conditions
func DownloadLogs(ctx *gin.Context, path string) *errors.Error {
	if strings.Contains(path, "..") || !strings.HasSuffix(path, ".jsonl") {
		return errors.Verify("invalid log file")
	}

	logRoot, err := filepath.Abs(getLogDir(""))
	if err != nil {
		return errors.Verify(fmt.Sprintf("resolve log root error: %v", err))
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return errors.Verify(fmt.Sprintf("invalid log path: %v", err))
	}
	if absPath != logRoot && !strings.HasPrefix(absPath, logRoot+string(os.PathSeparator)) {
		return errors.Verify("log file outside of log directory")
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return errors.Verify("log file does not exist")
	}

	ctx.Header("Content-Type", "application/octet-stream")
	ctx.Header("Content-Disposition", "attachment; filename="+filepath.Base(absPath))

	ctx.File(absPath)
	return nil
}
