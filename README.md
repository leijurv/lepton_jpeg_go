This is a Go port of [microsoft/lepton_jpeg_rust](https://github.com/microsoft/lepton_jpeg_rust) (which itself was a port of [dropbox/lepton](https://github.com/dropbox/lepton)).

This was written entirely by Claude and Codex.

After it passed all Rust's decompressor tests, I tested it on 97,235 lepton files (including every photo I've ever taken) from [gb](https://github.com/leijurv/gb) and it was able to decompress all but 4 correctly. Got Claude to fix those, but then an additional 2 decompressed wrong, and then after fixing that, one more broke. So this really doesn't inspire confidence. I only use it because I have it round-trip verify every compression, so I know the decompressor works on each file as it's written.
