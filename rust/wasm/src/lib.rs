use std::io::Cursor;
use wasm_bindgen::prelude::*;

pub use wasm_bindgen_rayon::init_thread_pool;

use lepton_jpeg::{EnabledFeatures, RayonThreadPool};

/// Decode lepton data back to JPEG
#[wasm_bindgen]
pub fn decode_lepton(data: &[u8], num_threads: usize) -> Result<Vec<u8>, JsError> {
    let mut reader = Cursor::new(data);
    let mut output = Vec::new();
    let features = EnabledFeatures::compat_lepton_vector_read();

    let pool = RayonThreadPool::new(num_threads.max(1));
    lepton_jpeg::decode_lepton(&mut reader, &mut output, &features, &pool)
        .map_err(|e| JsError::new(&e.to_string()))?;

    Ok(output)
}

/// Encode JPEG to lepton format
#[wasm_bindgen]
pub fn encode_lepton(jpeg_data: &[u8], num_threads: usize) -> Result<Vec<u8>, JsError> {
    let mut reader = Cursor::new(jpeg_data);
    let mut output = Cursor::new(Vec::new());
    let features = EnabledFeatures::compat_lepton_vector_write();

    let pool = RayonThreadPool::new(num_threads.max(1));
    lepton_jpeg::encode_lepton(&mut reader, &mut output, &features, &pool)
        .map_err(|e| JsError::new(&e.to_string()))?;

    Ok(output.into_inner())
}
