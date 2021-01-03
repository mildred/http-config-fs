*Warning, this software is experimental may contain yet undetected vulnerabilities. Use at your own risk.*

# Usage

```bash
$ http-config-fs http://localhost:80/ /path/to/mountpoint
```

# Caveats

- I wrote this for the purpose of exposing dynamic JSON files from an API to local filesystem to allow configuration reload in Exim. If it doesn't work for your use case, open an issue or submit a Pull Request.
- Generates **a lot** of HTTP requests. You probably don't want to be using this on any infrastructure you don't own.

# Library credits

- [Go Programming Language](https://golang.org/)
- [FUSE Go bindings](https://github.com/hanwen/go-fuse) provides the filesystem virtualization interface
