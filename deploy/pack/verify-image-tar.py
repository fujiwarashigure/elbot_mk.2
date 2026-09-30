#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""不依赖 Docker，离线校验 docker save 格式镜像 tar.gz 的结构与摘要。"""
import gzip  # noqa: F401  (tarfile 会自动处理 gz)
import hashlib
import io
import json
import struct
import sys
import tarfile
from pathlib import Path


def sha(b: bytes) -> str:
    return hashlib.sha256(b).hexdigest()


ELF_MACHINE = {0x3E: "amd64", 0xB7: "arm64"}


def verify(path: str) -> dict:
    with tarfile.open(path, "r:gz") as outer:
        names = outer.getnames()

        def read(name: str) -> bytes:
            f = outer.extractfile(name)
            if f is None:
                raise AssertionError("missing member: " + name)
            return f.read()

        assert "manifest.json" in names, "manifest.json missing"
        manifest = json.loads(read("manifest.json"))
        assert isinstance(manifest, list) and manifest, "manifest.json invalid"
        item = manifest[0]

        config_bytes = read(item["Config"])
        config = json.loads(config_bytes)
        assert sha(config_bytes) == Path(item["Config"]).stem, "config filename != sha256(config)"
        assert item["RepoTags"] == ["elbot:0.5.0"], item["RepoTags"]

        layers = item["Layers"]
        diff_ids = config["rootfs"]["diff_ids"]
        assert len(layers) == len(diff_ids), "manifest layers != rootfs diff_ids"
        assert config["rootfs"]["type"] == "layers", config["rootfs"]["type"]
        assert config["os"] == "linux", config["os"]

        # history：非空层数量必须等于 diff_ids 数量
        non_empty = [h for h in config.get("history", []) if not h.get("empty_layer", False)]
        assert len(non_empty) == len(diff_ids), \
            "history non-empty layers (%d) != diff_ids (%d)" % (len(non_empty), len(diff_ids))

        layer_path = layers[0]
        assert layer_path.endswith("/layer.tar"), layer_path
        layer_dir = layer_path.split("/")[0]
        assert layer_dir + "/VERSION" in names, "layer VERSION missing"
        assert layer_dir + "/json" in names, "layer json missing"
        layer_meta = json.loads(read(layer_dir + "/json"))
        assert layer_meta.get("id") == layer_dir, "layer json id mismatch"

        layer_bytes = read(layer_path)
        diff_id = "sha256:" + sha(layer_bytes)
        assert diff_id == diff_ids[0], "layer diff_id mismatch"

        # 层内容
        with tarfile.open(fileobj=io.BytesIO(layer_bytes), mode="r:") as layer:
            lnames = layer.getnames()
            for required in (
                "usr/local/bin/elbot",
                "etc/ssl/certs/ca-certificates.crt",
                "etc/passwd",
                "etc/group",
                "usr/share/zoneinfo/Asia/Shanghai",
                "tmp",
                "data",
            ):
                assert required in lnames, "layer missing: " + required
            bininfo = layer.getmember("usr/local/bin/elbot")
            assert bininfo.mode & 0o111, "binary not executable"
            data_dir = layer.getmember("data")
            assert data_dir.uid == 10001 and data_dir.gid == 10001, "data dir uid/gid wrong"
            bin_data = layer.extractfile("usr/local/bin/elbot").read()
            assert bin_data[:4] == b"\x7fELF", "not an ELF binary"
            machine = struct.unpack("<H", bin_data[18:20])[0]
            elf_arch = ELF_MACHINE.get(machine)
            assert elf_arch == config["architecture"], \
                "ELF arch %s != config arch %s" % (elf_arch, config["architecture"])

        cfg = config["config"]
        assert cfg.get("Entrypoint") == ["/usr/local/bin/elbot"], cfg.get("Entrypoint")
        assert cfg.get("Cmd") == ["service", "run"], cfg.get("Cmd")
        assert "/data" in cfg.get("Volumes", {}), "volume /data missing"

        return {
            "file": str(path),
            "tag": item["RepoTags"][0],
            "architecture": config["architecture"],
            "os": config["os"],
            "user": cfg.get("User"),
            "entrypoint": cfg.get("Entrypoint"),
            "cmd": cfg.get("Cmd"),
            "volumes": list(cfg.get("Volumes", {}).keys()),
            "exposed": list(cfg.get("ExposedPorts", {}).keys()),
            "diff_id": diff_id,
            "config_digest": sha(config_bytes),
            "binary_bytes": len(bin_data),
            "layer_members": len(lnames),
            "ELF_arch": elf_arch,
            "OK": True,
        }


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit("usage: verify-image-tar.py <image.tar.gz> [...]")
    for p in sys.argv[1:]:
        print(json.dumps(verify(p), indent=2, ensure_ascii=False))
