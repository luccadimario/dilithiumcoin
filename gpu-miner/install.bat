@echo off
REM Dilithium GPU Miner Installation Script for Windows

echo =========================================================
echo    DILITHIUM GPU MINER INSTALLER (Windows)
echo    Version 1.0.0
echo =========================================================
echo.

REM Check for CUDA
echo [*] Checking for CUDA...
where nvcc >nul 2>&1
if %ERRORLEVEL% NEQ 0 (
    echo     X CUDA not found!
    echo.
    echo Please install CUDA Toolkit from:
    echo   https://developer.nvidia.com/cuda-downloads
    echo.
    pause
    exit /b 1
) else (
    for /f "tokens=*" %%i in ('nvcc --version ^| findstr "release"') do set CUDA_INFO=%%i
    echo     √ CUDA found: %CUDA_INFO%
)
echo.

REM Check for Rust
echo [*] Checking for Rust...
where cargo >nul 2>&1
if %ERRORLEVEL% NEQ 0 (
    echo     X Rust not found!
    echo.
    echo Please install Rust from:
    echo   https://rustup.rs/
    echo.
    echo After installation, restart this script.
    pause
    exit /b 1
) else (
    for /f "tokens=2" %%i in ('cargo --version') do set RUST_VERSION=%%i
    echo     √ Rust %RUST_VERSION% found
)
echo.

REM Check for Visual Studio Build Tools
echo [*] Checking for MSVC...
where cl >nul 2>&1
if %ERRORLEVEL% NEQ 0 (
    echo     X MSVC not found!
    echo.
    echo Please install Visual Studio Build Tools with C++ support:
    echo   https://visualstudio.microsoft.com/downloads/
    echo.
    echo Make sure to select "Desktop development with C++" workload.
    pause
    exit /b 1
) else (
    echo     √ MSVC found
)
echo.

REM Build the miner
echo [*] Building GPU miner...
cargo build --release
if %ERRORLEVEL% NEQ 0 (
    echo     X Build failed!
    pause
    exit /b 1
)
echo     √ Build successful
echo.

REM Create installation directory
set INSTALL_DIR=%ProgramFiles%\Dilithium\GPU-Miner
echo [*] Installing to %INSTALL_DIR%...
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"

REM Copy binary and web assets
copy /Y target\release\dilithium-gpu-miner.exe "%INSTALL_DIR%\" >nul
xcopy /Y /I static "%INSTALL_DIR%\static\" >nul 2>&1
copy /Y README.md "%INSTALL_DIR%\" >nul 2>&1
echo     √ Files installed
echo.

REM Add to PATH
echo [*] Adding to system PATH...
setx PATH "%PATH%;%INSTALL_DIR%" >nul 2>&1
echo     √ PATH updated (restart terminal for changes to take effect)
echo.

REM Create start menu shortcut (optional)
set /p CREATE_SHORTCUT="Create desktop shortcut? (Y/N): "
if /i "%CREATE_SHORTCUT%"=="Y" (
    set SCRIPT="%TEMP%\create_shortcut.vbs"
    echo Set oWS = WScript.CreateObject("WScript.Shell") > %SCRIPT%
    echo sLinkFile = "%USERPROFILE%\Desktop\Dilithium GPU Miner.lnk" >> %SCRIPT%
    echo Set oLink = oWS.CreateShortcut(sLinkFile) >> %SCRIPT%
    echo oLink.TargetPath = "%INSTALL_DIR%\dilithium-gpu-miner.exe" >> %SCRIPT%
    echo oLink.WorkingDirectory = "%INSTALL_DIR%" >> %SCRIPT%
    echo oLink.Description = "Dilithium GPU Miner" >> %SCRIPT%
    echo oLink.Save >> %SCRIPT%
    cscript /nologo %SCRIPT%
    del %SCRIPT%
    echo     √ Desktop shortcut created
)
echo.

echo =========================================================
echo    INSTALLATION COMPLETE!
echo =========================================================
echo.
echo Usage:
echo   dilithium-gpu-miner --address YOUR_WALLET --node http://localhost:8080
echo.
echo Web dashboard will be available at:
echo   http://127.0.0.1:8080
echo.
echo For more options:
echo   dilithium-gpu-miner --help
echo.
echo Press any key to exit...
pause >nul
