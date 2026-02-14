use std::env;
use std::path::PathBuf;
use std::process::Command;

fn main() {
    println!("cargo:rerun-if-changed=cuda/");
    println!("cargo:rerun-if-changed=metal/");

    // Only compile CUDA when the cuda feature is enabled
    if std::env::var("CARGO_FEATURE_CUDA").is_ok() {
        build_cuda();
    }
}

fn build_cuda() {
    // CUDA toolkit detection
    let cuda_path = if cfg!(target_os = "windows") {
        env::var("CUDA_PATH")
            .map(PathBuf::from)
            .unwrap_or_else(|_| PathBuf::from(r"C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v11.8"))
    } else {
        PathBuf::from("/usr/local/cuda")
    };

    let cuda_lib = if cfg!(target_os = "windows") {
        cuda_path.join("lib").join("x64")
    } else {
        cuda_path.join("lib64")
    };

    // SM architecture — override with CUDA_SM env var if needed
    let sm_arch = env::var("CUDA_SM").unwrap_or_else(|_| "sm_86".to_string());
    let sm_arch = if sm_arch.starts_with("sm_") { sm_arch } else { format!("sm_{}", sm_arch) };

    println!("cargo:rustc-link-search=native={}", cuda_lib.display());
    println!("cargo:rustc-link-lib=cudart");

    let out_dir = env::var("OUT_DIR").unwrap();

    // Compile CUDA bridge using nvcc directly to avoid cc-rs host compiler issues.
    // cc-rs forces -ccbin=c++ on Linux which pulls in g++-13 C++ headers
    // (type_traits, mathcalls.h) that use _Float32 — a type NVCC can't parse.
    if cfg!(target_os = "windows") {
        // Windows: use cc-rs with MSVC (no _Float32 issue)
        cc::Build::new()
            .cuda(true)
            .flag("-allow-unsupported-compiler")
            .flag("-D_ALLOW_COMPILER_AND_STL_VERSION_MISMATCH")
            .flag(&format!("-arch={}", sm_arch))
            .flag("-O3")
            .flag("--use_fast_math")
            .file("cuda/bridge.cu")
            .compile("gpuminer");
    } else {
        // Linux/macOS: invoke nvcc directly with gcc as host compiler
        let obj_path = format!("{}/bridge.o", out_dir);

        let mut cmd = Command::new("nvcc");
        cmd.args([
            "-ccbin=gcc",
            "-allow-unsupported-compiler",
            &format!("-arch={}", sm_arch),
            "-O3",
            "--use_fast_math",
            "-Xcompiler", "-fPIC",
            "-Xcompiler", "-ffunction-sections",
            "-Xcompiler", "-fdata-sections",
            "-c", "cuda/bridge.cu",
            "-o", &obj_path,
        ]);

        let status = cmd
            .status()
            .expect("Failed to run nvcc — is the CUDA Toolkit installed?");

        if !status.success() {
            panic!("nvcc compilation failed");
        }

        // Create static library from the object file
        let lib_path = format!("{}/libgpuminer.a", out_dir);
        let ar_status = Command::new("ar")
            .args(["rcs", &lib_path, &obj_path])
            .status()
            .expect("Failed to run ar");

        if !ar_status.success() {
            panic!("ar failed to create static library");
        }

        println!("cargo:rustc-link-search=native={}", out_dir);
        println!("cargo:rustc-link-lib=static=gpuminer");
        println!("cargo:rustc-link-lib=stdc++");
    }

    println!("cargo:warning=CUDA miner build complete for {}", sm_arch);
}
