# Lepton WASM Build

This crate builds the lepton_jpeg library as a WebAssembly module with multi-threading support via wasm-bindgen-rayon.

## Prerequisites

- Rust nightly toolchain with `rust-src` component
- wasm-pack

```bash
rustup toolchain install nightly
rustup component add rust-src --toolchain nightly
cargo install wasm-pack
```

## Building

```bash
cd rust/wasm
RUSTUP_TOOLCHAIN=nightly wasm-pack build --target web --no-opt .
```

The `--no-opt` flag skips wasm-opt which can sometimes cause issues. You can remove it for smaller output if it works.

## Output

The build produces files in `pkg/`:
- `lepton_rust.js` - JavaScript bindings
- `lepton_rust_bg.wasm` - WebAssembly module with shared memory
- `snippets/wasm-bindgen-rayon-*/src/workerHelpers.no-bundler.js` - Worker helper for thread pool

## Copying to gb/webshare

```bash
cp pkg/lepton_rust_bg.wasm /path/to/gb/webshare/lepton/lepton_rust.wasm
cp pkg/lepton_rust.js /path/to/gb/webshare/lepton/lepton_rust.js
```

Then edit `lepton_rust.js` to fix the wasm filename:
```javascript
// Change this:
module_or_path = new URL('lepton_rust_bg.wasm', import.meta.url);
// To this:
module_or_path = new URL('lepton_rust.wasm', import.meta.url);
```

The `workerHelpers.no-bundler.js` in gb/webshare has been modified to spawn workers from URL directly (not blob) to inherit cross-origin isolation from the service worker. Don't overwrite it with the generated version.

## Key Configuration

### .cargo/config.toml

The critical piece for shared memory support:

```toml
[target.wasm32-unknown-unknown]
rustflags = [
  "-C", "target-feature=+atomics,+bulk-memory",
  "-C", "link-arg=--shared-memory",
  "-C", "link-arg=--max-memory=1073741824",
  "-C", "link-arg=--import-memory",
  "-C", "link-arg=--export=__wasm_init_tls",
  "-C", "link-arg=--export=__tls_size",
  "-C", "link-arg=--export=__tls_align",
  "-C", "link-arg=--export=__tls_base",
]

[unstable]
build-std = ["panic_abort", "std"]
```

Without `--shared-memory`, the WASM memory won't be a `SharedArrayBuffer` and thread pool initialization will fail with "Memory could not be cloned".

### Cargo.toml

The `no-bundler` feature is required for wasm-bindgen-rayon to generate the `mainJS()` method:

```toml
wasm-bindgen-rayon = { version = "1.2", features = ["no-bundler"] }
```

## Verifying Shared Memory

Check that the built WASM has shared memory:

```bash
wasm-tools print pkg/lepton_rust_bg.wasm | grep memory
```

Should show something like:
```
(import "wbg" "memory" (memory (;0;) 19 16384 shared))
```

The `shared` keyword indicates success. Without it, you'll get:
```
(memory (;0;) 18)
```
