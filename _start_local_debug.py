import os
import socket
import subprocess
import sys
import time
from pathlib import Path
from urllib.parse import urlparse


DETACHED_PROCESS = 0x00000008
CREATE_NEW_PROCESS_GROUP = 0x00000200


def _read_dotenv(path: Path) -> dict[str, str]:
    out: dict[str, str] = {}
    if not path.exists():
        return out
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        key = k.strip()
        val = v.strip().strip('"').strip("'")
        if key:
            out[key] = val
    return out


def _glm_base_url(glm_api_url: str) -> str:
    raw = (glm_api_url or "").strip()
    if not raw:
        return "https://open.bigmodel.cn/api/paas/v4"
    parsed = urlparse(raw)
    if not parsed.scheme or not parsed.netloc:
        return "https://open.bigmodel.cn/api/paas/v4"
    path = parsed.path.rstrip("/")
    if path.endswith("/chat/completions"):
        path = path[: -len("/chat/completions")]
    if not path:
        path = "/api/paas/v4"
    return f"{parsed.scheme}://{parsed.netloc}{path}"


def is_port_open(host: str, port: int, timeout: float = 0.5) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.settimeout(timeout)
        try:
            s.connect((host, port))
            return True
        except OSError:
            return False


def main() -> int:
    root = Path(__file__).resolve().parent
    go_dir = root / "go-service"
    vc_dir = root / "_tmp_valuecell" / "python"
    server_env = _read_dotenv(root / "server" / ".env")

    go_exe = go_dir / "finassistant_go.exe"
    go_main = go_dir / "main.go"
    need_build = (not go_exe.exists()) or (
        go_main.exists() and go_exe.exists() and go_main.stat().st_mtime > go_exe.stat().st_mtime
    )
    if need_build:
        print("[build] go-service changed, building finassistant_go.exe ...")
        build = subprocess.run(
            ["go", "build", "-o", "finassistant_go.exe", "."],
            cwd=str(go_dir),
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        )
        if build.returncode != 0:
            print("[error] go build failed")
            if build.stdout.strip():
                print(build.stdout.strip())
            if build.stderr.strip():
                print(build.stderr.strip())
            return 1
    if not go_exe.exists():
        print(f"[error] missing {go_exe}")
        return 1

    go_env = os.environ.copy()
    go_env["VALUECELL_API_URL"] = "http://127.0.0.1:8010/api/v1"
    go_env["VALUECELL_AGENT_NAME"] = ""

    if is_port_open("127.0.0.1", 3000):
        print("[ok] go backend already running on :3000, reuse existing process")
    else:
        go_log = open(root / "_go_backend.log", "w", encoding="utf-8")
        subprocess.Popen(
            [str(go_exe)],
            cwd=str(go_dir),
            env=go_env,
            stdout=go_log,
            stderr=subprocess.STDOUT,
            creationflags=DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP,
        )
        print("[ok] started go backend")

    if vc_dir.exists():
        vc_env = os.environ.copy()
        vc_env["ENV"] = "local_dev"
        vc_env["API_HOST"] = "127.0.0.1"
        vc_env["API_PORT"] = "8010"
        vc_env["PYTHONIOENCODING"] = "utf-8"
        glm_key = (vc_env.get("GLM_KEY") or server_env.get("GLM_KEY") or "").strip()
        glm_api = (
            vc_env.get("GLM_API_URL")
            or server_env.get("GLM_API_URL")
            or "https://open.bigmodel.cn/api/paas/v4/chat/completions"
        ).strip()
        llm_model = (vc_env.get("LLM_MODEL") or server_env.get("LLM_MODEL") or "glm-4-flash").strip()

        if glm_key:
            vc_env["OPENAI_COMPATIBLE_API_KEY"] = glm_key
        vc_env["OPENAI_COMPATIBLE_BASE_URL"] = _glm_base_url(glm_api)
        vc_env["AUTO_DETECT_PROVIDER"] = "false"
        vc_env["PRIMARY_PROVIDER"] = "openai-compatible"
        vc_env["SUPER_AGENT_PROVIDER"] = "openai-compatible"
        vc_env["RESEARCH_AGENT_PROVIDER"] = "openai-compatible"
        vc_env["PLANNER_MODEL_ID"] = llm_model
        vc_env["SUPER_AGENT_MODEL_ID"] = llm_model
        vc_env["RESEARCH_AGENT_MODEL_ID"] = llm_model
        if is_port_open("127.0.0.1", 8010):
            print("[ok] valuecell already running on :8010, reuse existing process")
        else:
            vc_log = open(root / "_valuecell.log", "w", encoding="utf-8")
            subprocess.Popen(
                [sys.executable, "-m", "valuecell.server.main"],
                cwd=str(vc_dir),
                env=vc_env,
                stdout=vc_log,
                stderr=subprocess.STDOUT,
                creationflags=DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP,
            )
            print("[ok] started valuecell")
    else:
        print(f"[warn] valuecell directory not found: {vc_dir}")

    time.sleep(8)
    print(f"[check] :3000 open={is_port_open('127.0.0.1', 3000)}")
    print(f"[check] :8010 open={is_port_open('127.0.0.1', 8010)}")
    print("[done] local debug services launched")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
