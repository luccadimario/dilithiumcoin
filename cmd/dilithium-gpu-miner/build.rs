use std::env;
use std::path::PathBuf;

fn main() {
    println!("cargo:rerun-if-changed=cuda/");

    // CUDA toolkit detection
    let cuda_path = if cfg!(target_os = "windows") {
        PathBuf::from(r"C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v11.8")
    } else {
        PathBuf::from("/usr/local/cuda")
    };

    let cuda_include = cuda_path.join("include");
    let cuda_lib = if cfg!(target_os = "windows") {
        cuda_path.join("lib").join("x64")
    } else {
        cuda_path.join("lib64")
    };

    // Compile CUDA files using nvcc
    let nvcc_path = if cfg!(target_os = "windows") {
        cuda_path.join("bin").join("nvcc.exe")
    } else {
        PathBuf::from("nvcc")
    };

    // SM architecture for RTX 3060 Ti
    let sm_arch = "sm_86";

    println!("cargo:rustc-link-search=native={}", cuda_lib.display());
    println!("cargo:rustc-link-lib=cudart");

    // Compile CUDA bridge
    cc::Build::new()
        .cuda(true)
        .flag("-allow-unsupported-compiler")
        .flag("-D_ALLOW_COMPILER_AND_STL_VERSION_MISMATCH")
        .flag("-D__STRICT_ANSI__")
        .flag(&format!("-arch={}", sm_arch))
        .flag("-O3")
        .flag("--use_fast_math")
        .file("cuda/bridge.cu")
        .compile("gpuminer");

    println!("cargo:warning=CUDA miner build complete for {}", sm_arch);
}
