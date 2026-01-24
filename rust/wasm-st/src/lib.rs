use std::io::Cursor;
use wasm_bindgen::prelude::*;

use lepton_jpeg::{EnabledFeatures, SingleThreadPool};

/// Decode lepton data back to JPEG (single-threaded version for browsers without SharedArrayBuffer)
#[wasm_bindgen]
pub fn decode_lepton(data: &[u8]) -> Result<Vec<u8>, JsError> {
    let mut reader = Cursor::new(data);
    let mut output = Vec::new();
    let features = EnabledFeatures::compat_lepton_vector_read();

    let pool = SingleThreadPool::default();
    lepton_jpeg::decode_lepton(&mut reader, &mut output, &features, &pool)
        .map_err(|e| JsError::new(&e.to_string()))?;

    Ok(output)
}

/// Encode JPEG to lepton format (single-threaded version for browsers without SharedArrayBuffer)
#[wasm_bindgen]
pub fn encode_lepton(jpeg_data: &[u8]) -> Result<Vec<u8>, JsError> {
    let mut reader = Cursor::new(jpeg_data);
    let mut output = Cursor::new(Vec::new());
    let features = EnabledFeatures::compat_lepton_vector_write();

    let pool = SingleThreadPool::default();
    lepton_jpeg::encode_lepton(&mut reader, &mut output, &features, &pool)
        .map_err(|e| JsError::new(&e.to_string()))?;

    Ok(output.into_inner())
}
