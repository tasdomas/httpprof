// Copyright 2010 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pprof serves via its HTTP server runtime profiling data
// in the format expected by the pprof visualization tool.
// For more information about pprof, see
// http://code.google.com/p/google-perftools/.
//
//
// For a study of the facility in action, visit
//
//	http://blog.golang.org/2011/06/profiling-go-programs.html
//
package pprof

import (
	"bufio"
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"
)

// Cmdline responds with the running program's
// command line, with arguments separated by NUL bytes.
// The package initialization registers it as /debug/pprof/cmdline.
func Cmdline(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, strings.Join(os.Args, "\x00"))
}

// Profile responds with the pprof-formatted cpu profile.
// The package initialization registers it as /debug/pprof/profile.
func Profile(w http.ResponseWriter, r *http.Request) {
	sec, _ := strconv.ParseInt(r.FormValue("seconds"), 10, 64)
	if sec == 0 {
		sec = 30
	}

	// Set Content Type assuming StartCPUProfile will work,
	// because if it does it starts writing.
	w.Header().Set("Content-Type", "application/octet-stream")
	if err := pprof.StartCPUProfile(w); err != nil {
		// StartCPUProfile failed, so no writes yet.
		// Can change header back to text content
		// and send error code.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Could not enable CPU profiling: %s\n", err)
		return
	}
	time.Sleep(time.Duration(sec) * time.Second)
	pprof.StopCPUProfile()
}

// Symbol looks up the program counters listed in the request,
// responding with a table mapping program counters to function names.
// The package initialization registers it as /debug/pprof/symbol.
func Symbol(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	// We have to read the whole POST body before
	// writing any output.  Buffer the output here.
	var buf bytes.Buffer

	// We don't know how many symbols we have, but we
	// do have symbol information.  Pprof only cares whether
	// this number is 0 (no symbols available) or > 0.
	fmt.Fprintf(&buf, "num_symbols: 1\n")

	var b *bufio.Reader
	if r.Method == "POST" {
		b = bufio.NewReader(r.Body)
	} else {
		b = bufio.NewReader(strings.NewReader(r.URL.RawQuery))
	}

	for {
		word, err := b.ReadSlice('+')
		if err == nil {
			word = word[0 : len(word)-1] // trim +
		}
		pc, _ := strconv.ParseUint(string(word), 0, 64)
		if pc != 0 {
			f := runtime.FuncForPC(uintptr(pc))
			if f != nil {
				fmt.Fprintf(&buf, "%#x %s\n", pc, f.Name())
			}
		}

		// Wait until here to check for err; the last
		// symbol will have an err because it doesn't end in +.
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(&buf, "reading request: %v\n", err)
			}
			break
		}
	}

	w.Write(buf.Bytes())
}

// Handler returns an HTTP handler that serves the named profile.
func Handler(name string) http.Handler {
	return handler(name)
}

type handler string

func (name handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	debug, _ := strconv.Atoi(r.FormValue("debug"))
	p := pprof.Lookup(string(name))
	if p == nil {
		w.WriteHeader(404)
		fmt.Fprintf(w, "Unknown profile: %s\n", name)
		return
	}
	gc, _ := strconv.Atoi(r.FormValue("gc"))
	if name == "heap" && gc > 0 {
		runtime.GC()
	}
	p.WriteTo(w, debug)
	return
}

// Index responds with the pprof-formatted profile named by the request.
// For example, "/debug/pprof/heap" serves the "heap" profile.
// Index responds to a request for "/debug/pprof/" with an HTML page
// listing the available profiles.
func Index(w http.ResponseWriter, r *http.Request) {
	index("/debug/pprof/").ServeHTTP(w, r)
}

var indexTmpl = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html>
<head>
  <title>pprof</title>
</head>
<body>
  <h1>pprof</h1>
  <h2>profiles:</h2>
  <table>
    {{range .}}
    <tr>
      <td style="text-align: right;">{{.Count}}</td>
      <td><a href="{{.Name}}?debug=1">{{.Name}}</a></td>
    </tr>
    {{end}}
  </table>
  <p><a href="goroutine?debug=2">full goroutine stack dump</a></p>
</body>
</html>
`))

// IndexAtRoot returns a handler that responds to an HTTP request
// at the given root path with an HTML page listing the available profiles.
// A trailing '/' will be added to root if there is not one already there.
func IndexAtRoot(root string) http.Handler {
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}
	return index(root)
}

// index is an http.Handler that is functionally equivilent to Index except
// that an arbitrary prefix may be used.
type index string

// ServeHTTP implements http.Handler. ServeHTTP responds with the pprof-
// formatted profile named by the request after the prefix specified in index
// has been stripped.
func (i index) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, string(i)) {
		name := strings.TrimPrefix(r.URL.Path, string(i))
		if name != "" {
			handler(name).ServeHTTP(w, r)
			return
		}
	}
	if err := indexTmpl.Execute(w, pprof.Profiles()); err != nil {
		log.Print(err)
	}
}
