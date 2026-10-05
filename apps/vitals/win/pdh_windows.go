//go:build windows

package main

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Minimal pdh.dll wrapper (docs/research/windows-system-stats.md §1): one
// long-lived query, English counter paths, formatted doubles. Rate counters
// need two collections before they yield data, so the first read after
// opening returns pdhInvalidData — callers treat that as "no data yet".

var (
	modpdh                           = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery                 = modpdh.NewProc("PdhOpenQuery")
	procPdhAddEnglishCounterW        = modpdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData          = modpdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterArrayW = modpdh.NewProc("PdhGetFormattedCounterArrayW")
	procPdhGetFormattedCounterValue  = modpdh.NewProc("PdhGetFormattedCounterValue")
	procPdhCloseQuery                = modpdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble          = 0x200
	pdhCStatusValidData   = 0
	pdhCStatusNewData     = 1
	pdhCStatusNoInstance  = 0x800007D1
	pdhMoreData           = 0x800007D2
	pdhNoData             = 0x800007D5
	pdhCStatusNoObject    = 0xC0000BB8
	pdhCStatusNoCounter   = 0xC0000BB9
	pdhCStatusInvalidData = 0xC0000BBA
	pdhInvalidData        = 0xC0000BC6
)

// pdhError is a PDH_STATUS other than success.
type pdhError uint32

func (e pdhError) Error() string { return fmt.Sprintf("pdh: status 0x%08X", uint32(e)) }

// noData reports the flow-control statuses that mean "nothing yet".
func (e pdhError) noData() bool {
	switch uint32(e) {
	case pdhInvalidData, pdhNoData, pdhCStatusNoInstance, pdhCStatusInvalidData:
		return true
	}
	return false
}

// PDH_FMT_COUNTERVALUE with the double member: 16 bytes, union at +8.
type pdhFmtCounterValueDouble struct {
	CStatus uint32
	_       uint32
	Value   float64
}

// PDH_FMT_COUNTERVALUE_ITEM_W: 24 bytes on amd64/arm64.
type pdhFmtCounterValueItemDouble struct {
	Name *uint16
	Fmt  pdhFmtCounterValueDouble
}

type pdhQuery struct {
	h uintptr
}

func openPdhQuery() (*pdhQuery, error) {
	var h uintptr
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&h))); r != 0 {
		return nil, pdhError(r)
	}
	return &pdhQuery{h: h}, nil
}

func (q *pdhQuery) Close() {
	if q != nil && q.h != 0 {
		procPdhCloseQuery.Call(q.h)
		q.h = 0
	}
}

// AddEnglishCounter adds a counter path (wildcards allowed) on any locale.
func (q *pdhQuery) AddEnglishCounter(path string) (uintptr, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var c uintptr
	if r, _, _ := procPdhAddEnglishCounterW.Call(q.h, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&c))); r != 0 {
		return 0, pdhError(r)
	}
	return c, nil
}

// Collect takes one sample for every counter of the query.
func (q *pdhQuery) Collect() error {
	if r, _, _ := procPdhCollectQueryData.Call(q.h); r != 0 {
		return pdhError(r)
	}
	return nil
}

// formattedArray reads every instance of a wildcard counter.
func formattedArray(counter uintptr) ([]pdhItem, error) {
	var size, count uint32
	var buf []byte
	r, _, _ := procPdhGetFormattedCounterArrayW.Call(counter, pdhFmtDouble, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	for i := 0; r == pdhMoreData && i < 4; i++ { // instances may appear between the two calls
		buf = make([]byte, size)
		r, _, _ = procPdhGetFormattedCounterArrayW.Call(counter, pdhFmtDouble, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
	}
	if r != 0 {
		return nil, pdhError(r)
	}
	if count == 0 || len(buf) == 0 {
		return nil, nil
	}
	items := unsafe.Slice((*pdhFmtCounterValueItemDouble)(unsafe.Pointer(&buf[0])), count)
	out := make([]pdhItem, 0, count)
	for i := range items {
		if s := items[i].Fmt.CStatus; s != pdhCStatusValidData && s != pdhCStatusNewData {
			continue
		}
		out = append(out, pdhItem{Instance: windows.UTF16PtrToString(items[i].Name), Value: items[i].Fmt.Value})
	}
	runtime.KeepAlive(buf)
	return out, nil
}

// formattedValue reads a single-instance counter.
func formattedValue(counter uintptr) (float64, error) {
	var v pdhFmtCounterValueDouble
	if r, _, _ := procPdhGetFormattedCounterValue.Call(counter, pdhFmtDouble, 0, uintptr(unsafe.Pointer(&v))); r != 0 {
		return 0, pdhError(r)
	}
	if v.CStatus != pdhCStatusValidData && v.CStatus != pdhCStatusNewData {
		return 0, pdhError(v.CStatus)
	}
	return v.Value, nil
}
