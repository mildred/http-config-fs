package main

import (
	"context"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

var logv *log.Logger
var logd *log.Logger

func isDirRedirect(url, location *url.URL) (result bool) {
	result = url.Host == location.Host &&
		url.Path+"/" == location.Path
	logd.Printf("isDirRedirect(%v, %v) = %v", url, location, result)
	return
}

var HttpClientNoRedirects *http.Client

type httpHandle struct {
	resp       *http.Response
	hasContent bool
	content    []byte
	contentErr syscall.Errno
}

func (fh *httpHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	logv.Printf("Read(seek=%d, len=%d)", off, len(dest))

	if !fh.hasContent {
		var err error
		fh.content, err = ioutil.ReadAll(fh.resp.Body)
		fh.hasContent = true
		if err != nil {
			fh.contentErr = syscall.EIO
		}
	}

	if fh.contentErr != 0 {
		return nil, fh.contentErr
	}

	end := off + int64(len(dest))
	if end > int64(len(fh.content)) {
		end = int64(len(fh.content))
	}

	// We could copy to the `dest` buffer, but since we have a
	// []byte already, return that.
	return fuse.ReadResultData(fh.content[off:end]), 0
}

var _ = (fs.FileReader)((*httpHandle)(nil))

type urlNode struct {
	fs.Inode
	Flat       bool
	Extensions bool
	URL        *url.URL
}

// Lookup is part of the NodeLookuper interface
func (n *urlNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	logv.Printf("Lookup(node=%s, name=%s)", n.URL.String(), name)
	url, err := n.URL.Parse(name)
	if err != nil {
		return nil, syscall.ENOENT
	}

	var mode uint32 = fuse.S_IFREG
	if n.Extensions && !strings.Contains(name, ".") {
		mode = fuse.S_IFDIR
	} else if !n.Flat {
		resp, err := HttpClientNoRedirects.Head(url.String())
		if err != nil {
			log.Printf("Lookup(url=%v) error: %e", url, err)
		} else if loc, err := resp.Location(); err == nil && isDirRedirect(url, loc) {
			logv.Printf("Lookup(url=%v) detected as directory: redirect to %s", url, loc)
			mode = fuse.S_IFDIR
		}
	}

	if mode == fuse.S_IFDIR {
		url.Path = url.Path + "/"
	}

	stable := fs.StableAttr{
		Mode: mode,
	}
	operations := &urlNode{URL: url}

	// The NewInode call wraps the `operations` object into an Inode.
	child := n.NewInode(ctx, operations, stable)

	// In case of concurrent lookup requests, it can happen that operations !=
	// child.Operations().
	return child, 0
}

func (n *urlNode) Open(ctx context.Context, openFlags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	logv.Printf("Open(node=%s)", n.URL.String())

	// disallow writes
	if fuseFlags&(syscall.O_RDWR|syscall.O_WRONLY) != 0 {
		return nil, 0, syscall.EROFS
	}

	// Make request
	resp, err := http.Get(n.URL.String())
	err_no := syscall.Errno(0)

	if err != nil {
		log.Printf("Error in Open(node=%s): %e", n.URL.String(), err)
		err_no = syscall.EIO
	}

	fh = &httpHandle{
		resp:       resp,
		hasContent: err_no != 0,
		content:    []byte{},
		contentErr: err_no,
	}

	// Return FOPEN_DIRECT_IO so content is not cached.
	return fh, fuse.FOPEN_DIRECT_IO, 0
}

var _ = (fs.NodeLookuper)((*urlNode)(nil))
var _ = (fs.NodeOpener)((*urlNode)(nil))

func main() {
	verbose := flag.Bool("v", false, "Verbose")
	debug := flag.Bool("debug", false, "Debug FUSE")
	flat := flag.Bool("flat", false, "Consider there is no directories (one level hierarchy only)")
	extensions := flag.Bool("extensions", false, "Consider directories are the files without extension")
	flag.Parse()
	if len(flag.Args()) < 1 {
		log.Fatal("Usage: http-config-fs <http url> <mount point>")
		os.Exit(1)
	}

	logv = log.New(os.Stderr, "[verbose] ", log.LstdFlags)
	logd = log.New(os.Stderr, "[debug] ", log.LstdFlags)
	if !*verbose {
		logv.SetOutput(ioutil.Discard)
	}
	if !*debug {
		logd.SetOutput(ioutil.Discard)
	}

	exec.Command("/bin/fusermount", "-uz", flag.Arg(1)).Run()

	srcURL, err := url.Parse(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	HttpClientNoRedirects = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Allow https redirects
			if req.URL.Scheme == "https" && via[len(via)-1].URL.Scheme == "http" {
				return nil
			}
			// Block other redirects
			return http.ErrUseLastResponse
		},
	}

	root := &urlNode{
		URL:        srcURL,
		Flat:       *flat,
		Extensions: *extensions,
	}
	mntDir := flag.Arg(1)

	log.Printf("Mounting %v to %s", root.URL, mntDir)
	server, err := fs.Mount(mntDir, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			// Set to true to see how the file system works.
			Debug: *debug,
		},
	})
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	log.Printf("Unmount by calling 'fusermount -u %s'", mntDir)

	// Wait until unmount before exiting
	server.Wait()
}
