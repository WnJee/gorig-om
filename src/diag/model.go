package diag

import "time"

type GoroutineStackFrame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

type GoroutineInfo struct {
	ID           int64                 `json:"id"`
	State        string                `json:"state"`
	WaitDuration string                `json:"waitDuration,omitempty"`
	Frames       []GoroutineStackFrame `json:"frames"`
}

type GoroutineGroup struct {
	Count      int                   `json:"count"`
	Percentage float64               `json:"percentage"`
	State      string                `json:"state"`
	TopFrame   GoroutineStackFrame   `json:"topFrame"`
	Frames     []GoroutineStackFrame `json:"frames"`
}

type GoroutineClusterResult struct {
	TotalGoroutines int              `json:"totalGoroutines"`
	TotalGroups     int              `json:"totalGroups"`
	Timestamp       time.Time        `json:"timestamp"`
	Groups          []GoroutineGroup `json:"groups"`
}
