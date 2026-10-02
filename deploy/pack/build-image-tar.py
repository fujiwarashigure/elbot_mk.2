#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
离线构造标准 `docker save` 格式的 ElBot 镜像 tar.gz。

不需要 Docker 守护进程，也不需要联网：
  - 输入一个静态 Linux 二进制（CGO_ENABLED=0，已用 Go 交叉编译）
  - 组装最小 rootfs（二进制 + CA 证书 + /etc/passwd + 时区数据）
  - 生成单层 Docker 镜像归档，服务器上 docker load -i 即可

用法：
  python build-image-tar.py --binary ./elbot-linux-amd64 --arch amd64 \
      --version 0.6.5 --output elbot-0.6.5-linux-amd64.tar.gz

注意：scratch 镜像内没有 /bin/sh 和 coreutils，ElBot 的 shell 工具不可用；
需要完整工具能力请用同目录的 Dockerfile（基于 debian:bookworm-slim）在服务器上构建。
"""

import argparse
import gzip
import hashlib
import io
import json
import os
import shutil
import sys
import tarfile
import tempfile
import time
from pathlib import Path

UID_ELBOT = 10001
GID_ELBOT = 10001
CREATED = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def add_dir(tar: tarfile.TarFile, name: str, mode: int = 0o755,
            uid: int = 0, gid: int = 0, mtime: int = 0):
    ti = tarfile.TarInfo(name.rstrip("/") + "/")
    ti.type = tarfile.DIRTYPE
    ti.mode = mode
    ti.uid = uid
    ti.gid = gid
    ti.uname = "root"
    ti.gname = "root"
    ti.mtime = mtime
    tar.addfile(ti)


def add_bytes(tar: tarfile.TarFile, name: str, data: bytes, mode: int = 0o644,
              uid: int = 0, gid: int = 0, mtime: int = 0):
    ti = tarfile.TarInfo(name)
    ti.size = len(data)
    ti.mode = mode
    ti.uid = uid
    ti.gid = gid
    ti.uname = "root"
    ti.gname = "root"
    ti.mtime = mtime
    tar.addfile(ti, io.BytesIO(data))


def add_file(tar: tarfile.TarFile, arcname: str, src: Path, mode: int,
             uid: int = 0, gid: int = 0, mtime: int = 0):
    st = os.stat(src)
    ti = tarfile.TarInfo(arcname)
    ti.size = st.st_size
    ti.mode = mode
    ti.uid = uid
    ti.gid = gid
    ti.uname = "root"
    ti.gname = "root"
    ti.mtime = mtime
    with open(src, "rb") as f:
        tar.addfile(ti, f)


CA_CANDIDATES = (
    "/etc/ssl/certs/ca-certificates.crt",
    "/etc/pki/tls/certs/ca-bundle.crt",
    "/etc/ssl/cert.pem",
)


def resolve_ca_bundle(explicit: str = "") -> Path:
    """定位 CA 证书 bundle。

    顺序：显式指定 -> 系统自带路径 -> python 的 certifi。
    这样在干净机器上不再硬依赖 `pip install certifi`。
    """
    if explicit:
        p = Path(explicit)
        if p.is_file():
            return p
        sys.exit("--ca-bundle 指定的文件不存在: " + explicit)
    for cand in CA_CANDIDATES:
        p = Path(cand)
        if p.is_file():
            return p
    try:
        import certifi  # type: ignore
    except Exception:
        certifi = None
    if certifi is not None:
        p = Path(certifi.where())
        if p.is_file():
            return p
    sys.exit(
        "找不到 CA 证书 bundle：请安装 ca-certificates，或用 --ca-bundle 指定，"
        "或 pip install certifi"
    )


def stage_rootfs(binary: Path, staging: Path, zoneinfo_src, ca_bundle: Path):
    # 二进制
    (staging / "usr/local/bin").mkdir(parents=True, exist_ok=True)
    shutil.copy2(binary, staging / "usr/local/bin/elbot")
    os.chmod(staging / "usr/local/bin/elbot", 0o755)

    # CA 证书（优先系统 bundle，不硬依赖 certifi）
    (staging / "etc/ssl/certs").mkdir(parents=True, exist_ok=True)
    shutil.copy2(ca_bundle, staging / "etc/ssl/certs/ca-certificates.crt")

    # 用户信息
    (staging / "etc").mkdir(parents=True, exist_ok=True)
    (staging / "etc/passwd").write_text(
        "root:x:0:0:root:/root:/sbin/nologin\n"
        "elbot:x:10001:10001:ElBot:/home/elbot:/usr/sbin/nologin\n",
        encoding="utf-8",
    )
    (staging / "etc/group").write_text(
        "root:x:0:\nelbot:x:10001:\n",
        encoding="utf-8",
    )

    # 时区数据（没有则退化成 UTC）
    if zoneinfo_src and Path(zoneinfo_src).exists():
        dst = staging / "usr/share/zoneinfo"
        shutil.copytree(
            Path(zoneinfo_src),
            dst,
            ignore=shutil.ignore_patterns("*.py", "__pycache__"),
            symlinks=False,
        )
        shanghai = dst / "Asia/Shanghai"
        if shanghai.exists():
            shutil.copy2(shanghai, staging / "etc/localtime")

    # 工作目录 / 数据目录 / tmp
    (staging / "home/elbot").mkdir(parents=True, exist_ok=True)
    (staging / "data/run").mkdir(parents=True, exist_ok=True)
    (staging / "data/cache").mkdir(parents=True, exist_ok=True)
    (staging / "tmp").mkdir(parents=True, exist_ok=True)
    os.chmod(staging / "tmp", 0o1777)


def build_layer(staging: Path, out_path: Path):
    mtime = 0
    with tarfile.open(out_path, "w", format=tarfile.GNU_FORMAT) as tar:
        add_dir(tar, "usr", mtime=mtime)
        add_dir(tar, "usr/local", mtime=mtime)
        add_dir(tar, "usr/local/bin", mtime=mtime)
        add_dir(tar, "usr/share", mtime=mtime)
        add_dir(tar, "etc", mtime=mtime)
        add_dir(tar, "etc/ssl", mtime=mtime)
        add_dir(tar, "etc/ssl/certs", mtime=mtime)
        add_dir(tar, "tmp", mode=0o1777, mtime=mtime)
        add_dir(tar, "home", mtime=mtime)
        add_dir(tar, "home/elbot", uid=UID_ELBOT, gid=GID_ELBOT, mtime=mtime)
        add_dir(tar, "data", uid=UID_ELBOT, gid=GID_ELBOT, mtime=mtime)
        add_dir(tar, "data/run", uid=UID_ELBOT, gid=GID_ELBOT, mtime=mtime)
        add_dir(tar, "data/cache", uid=UID_ELBOT, gid=GID_ELBOT, mtime=mtime)

        add_file(tar, "usr/local/bin/elbot", staging / "usr/local/bin/elbot",
                 mode=0o755, mtime=mtime)
        add_file(tar, "etc/ssl/certs/ca-certificates.crt",
                 staging / "etc/ssl/certs/ca-certificates.crt", mode=0o644, mtime=mtime)
        add_file(tar, "etc/passwd", staging / "etc/passwd", mode=0o644, mtime=mtime)
        add_file(tar, "etc/group", staging / "etc/group", mode=0o644, mtime=mtime)
        if (staging / "etc/localtime").exists():
            add_file(tar, "etc/localtime", staging / "etc/localtime",
                     mode=0o644, mtime=mtime)

        zi = staging / "usr/share/zoneinfo"
        if zi.exists():
            add_dir(tar, "usr/share/zoneinfo", mtime=mtime)
            for p in sorted(zi.rglob("*"), key=lambda x: str(x)):
                rel = "usr/share/zoneinfo/" + str(p.relative_to(zi)).replace("\\", "/")
                if p.is_dir():
                    add_dir(tar, rel, mtime=mtime)
                else:
                    add_file(tar, rel, p, mode=0o644, mtime=mtime)


def make_layer_tar(staging: Path, layer_path: Path) -> str:
    build_layer(staging, layer_path)
    return sha256_file(layer_path)


def make_config(arch: str, version: str, diff_id: str):
    return {
        "architecture": arch,
        "os": "linux",
        "created": CREATED,
        "config": {
            "Env": [
                "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                "TZ=Asia/Shanghai",
                "XDG_CONFIG_HOME=/data/config",
                "XDG_DATA_HOME=/data",
                "XDG_CACHE_HOME=/data/cache",
                "XDG_RUNTIME_DIR=/data/run",
                "ELBOT_HEALTH_ADDR=0.0.0.0:32171",
                "HOME=/home/elbot",
                "LANG=C.UTF-8",
            ],
            "Entrypoint": ["/usr/local/bin/elbot"],
            "Cmd": ["service", "run"],
            "WorkingDir": "/home/elbot",
            "User": "10001:10001",
            "ExposedPorts": {"32170/tcp": {}, "32171/tcp": {}, "32172/tcp": {}},
            "Volumes": {"/data": {}},
            "Healthcheck": {
                "Test": ["CMD", "/usr/local/bin/elbot", "--version"],
                "Interval": 30000000000,
                "Timeout": 5000000000,
                "StartPeriod": 30000000000,
                "Retries": 3,
            },
            "Labels": {
                "org.opencontainers.image.title": "elbot",
                "org.opencontainers.image.version": version,
            },
        },
        "history": [
            {
                "created": CREATED,
                "created_by": "elbot offline image packer",
                "comment": "elbot " + version,
            }
        ],
        "rootfs": {"type": "layers", "diff_ids": ["sha256:" + diff_id]},
    }


def make_image_archive(binary: Path, arch: str, version: str, tag: str,
                       output: Path, zoneinfo_src, ca_bundle: Path):
    with tempfile.TemporaryDirectory(prefix="elbot-image-") as tmp:
        tmpdir = Path(tmp)
        staging = tmpdir / "rootfs"
        staging.mkdir()
        stage_rootfs(binary, staging, zoneinfo_src, ca_bundle)

        layer_tar = tmpdir / "layer.tar"
        diff_id = make_layer_tar(staging, layer_tar)
        config = make_config(arch, version, diff_id)
        config_bytes = json.dumps(config, ensure_ascii=False, indent=2).encode("utf-8")
        config_hex = sha256_bytes(config_bytes)
        config_name = config_hex + ".json"
        layer_dir = diff_id

        manifest = [
            {
                "Config": config_name,
                "RepoTags": [tag],
                "Layers": [layer_dir + "/layer.tar"],
            }
        ]
        repositories = {"elbot": {tag.split(":")[-1]: config_hex}}
        layer_meta = {
            "id": layer_dir,
            "parent": "",
            "created": CREATED,
            "container_config": {},
            "config": {},
            "os": "linux",
        }

        raw_tar = tmpdir / "image.tar"
        with tarfile.open(raw_tar, "w", format=tarfile.GNU_FORMAT) as tar:
            add_dir(tar, layer_dir, mtime=0)
            add_file(tar, layer_dir + "/layer.tar", layer_tar, mode=0o644, mtime=0)
            add_bytes(tar, layer_dir + "/VERSION", b"1.0\n", mode=0o644, mtime=0)
            add_bytes(tar, layer_dir + "/json",
                      json.dumps(layer_meta, indent=2).encode("utf-8"), mode=0o644, mtime=0)
            add_bytes(tar, config_name, config_bytes, mode=0o644, mtime=0)
            add_bytes(tar, "manifest.json",
                      json.dumps(manifest, indent=2).encode("utf-8"), mode=0o644, mtime=0)
            add_bytes(tar, "repositories",
                      json.dumps(repositories, indent=2).encode("utf-8"), mode=0o644, mtime=0)

        output.parent.mkdir(parents=True, exist_ok=True)
        with open(raw_tar, "rb") as src, open(output, "wb") as raw_out, \
                gzip.GzipFile(fileobj=raw_out, mode="wb", compresslevel=6, mtime=0) as dst:
            shutil.copyfileobj(src, dst)

        return {
            "output": str(output),
            "tag": tag,
            "arch": arch,
            "diff_id": diff_id,
            "config_digest": config_hex,
            "layer_bytes": layer_tar.stat().st_size,
            "image_bytes": output.stat().st_size,
        }


def resolve_zoneinfo(explicit: str = "") -> Path:
    """定位 zoneinfo 目录。

    顺序：显式指定 -> 系统自带（Linux）-> python tzdata 包（Windows 等）。
    镜像里 TZ=Asia/Shanghai，如果缺 zoneinfo 就会退化成 UTC（定时报告会差 8 小时），
    而 verify-image-tar.py 也会因为缺 usr/share/zoneinfo/Asia/Shanghai 直接校验失败。
    所以在找不到时立刻给出明确错误，而不是生成一个必然校验不过的镜像。
    """
    def ok(path: Path) -> bool:
        return (path / "Asia" / "Shanghai").is_file()

    if explicit:
        p = Path(explicit)
        if ok(p):
            return p
        sys.exit("--zoneinfo 指定的目录里没有 Asia/Shanghai: " + explicit)
    for cand in ("/usr/share/zoneinfo", "/usr/lib/zoneinfo", "/etc/zoneinfo"):
        p = Path(cand)
        if ok(p):
            return p
    try:
        import tzdata  # type: ignore
    except Exception:
        tzdata = None
    if tzdata is not None:
        p = Path(tzdata.__file__).parent / "zoneinfo"
        if ok(p):
            return p
    sys.exit("找不到 zoneinfo（缺少 Asia/Shanghai）：请安装系统 tzdata，"
             "或用 --zoneinfo 指定目录，或 pip install tzdata")


def default_version() -> str:
    env_version = os.environ.get("ELBOT_VERSION", "").strip()
    if env_version:
        return env_version
    version_file = Path(__file__).resolve().parents[1] / "VERSION"
    if version_file.is_file():
        version = version_file.read_text(encoding="utf-8").strip()
        if version:
            return version
    return "dev"


def main():
    ap = argparse.ArgumentParser(description="构造 ElBot 离线 Docker 镜像 tar.gz")
    ap.add_argument("--binary", required=True, type=Path)
    ap.add_argument("--arch", default="amd64", choices=["amd64", "arm64"])
    ap.add_argument("--version", default=default_version())
    ap.add_argument("--tag", default=None, help="默认为 elbot:<version>")
    ap.add_argument("--output", required=True, type=Path)
    ap.add_argument("--zoneinfo", default=None, type=Path,
                    help="zoneinfo 目录；默认用系统 /usr/share/zoneinfo，再回退 python tzdata 包")
    ap.add_argument("--ca-bundle", default=None,
                    help="CA 证书 bundle 路径；默认按系统路径自动探测，最后才用 certifi")
    args = ap.parse_args()

    if not args.binary.is_file():
        sys.exit("binary not found: " + str(args.binary))

    ca_bundle = resolve_ca_bundle(args.ca_bundle or "")
    zoneinfo = resolve_zoneinfo(str(args.zoneinfo) if args.zoneinfo else "")

    tag = args.tag or ("elbot:" + args.version)
    info = make_image_archive(args.binary, args.arch, args.version, tag,
                              args.output, zoneinfo, ca_bundle)
    print(json.dumps(info, indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
