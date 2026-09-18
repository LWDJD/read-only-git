#!/usr/bin/env python3
"""把一个普通 git 仓库转换成可直接静态托管的裸仓库。

用法:
    python scripts/prepare-repo.py <源仓库> [输出目录] [仓库名]

参数:
    <源仓库>      必填。要转换的仓库路径，普通仓库和裸仓库都行。
    [输出目录]    默认取脚本旁边的 ../public。站点根目录，即放着 index.html 的那个。
    [仓库名]      默认取源目录名。带不带 .git 后缀等价。

产出:
    <输出目录>/<仓库名>.git/     dumb HTTP 协议所需的最小文件集
    <输出目录>/repository.json   仓库清单，已存在则合并

只依赖 Python 3 标准库和 PATH 里的 git，不依赖 shell 方言。
"""

from __future__ import annotations

import json
import re
import shutil
import subprocess
import sys
from pathlib import Path

GIT_SUFFIX = re.compile(r"(\.git)+$", re.IGNORECASE)

# dumb 协议不会请求这些，清掉让产物保持最小
JUNK_DIRS = ("hooks", "logs", "index", "COMMIT_EDITMSG", "description")
JUNK_FILES = (
    Path("info") / "exclude",
    Path("objects") / "info" / "commit-graph",
)
JUNK_PACK_SUFFIXES = (".bitmap", ".rev")

# 让 git 不要生成 dumb 协议用不到的辅助索引。
# 事后删也能做，但直接不生成更干净，也避开了删不掉的情况。
NO_AUX_INDEX = [
    "-c", "pack.writeBitmaps=false",
    "-c", "repack.writeBitmaps=false",
    "-c", "pack.writeReverseIndex=false",
    "-c", "gc.writeCommitGraph=false",
]

REQUIRED = (
    Path("HEAD"),
    Path("info") / "refs",
    Path("objects") / "info" / "packs",
)


class Failure(Exception):
    pass


def run_git(args: list[str], cwd: Path | None = None) -> str:
    """跑一条 git 命令，失败就抛异常。"""
    try:
        result = subprocess.run(
            ["git", *args],
            cwd=str(cwd) if cwd else None,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        )
    except FileNotFoundError:
        raise Failure("找不到 git，请确认它在 PATH 里")
    except OSError as exc:
        raise Failure(f"无法执行 git: {exc}")

    if result.returncode != 0:
        detail = (result.stderr or result.stdout or "").strip()
        raise Failure(f"git {' '.join(args)} 失败: {detail}")

    return result.stdout


def try_git(args: list[str], cwd: Path | None = None) -> tuple[bool, str]:
    try:
        return True, run_git(args, cwd)
    except Failure:
        return False, ""


def normalize_name(raw: str) -> str:
    """目录一律以 .git 结尾，对外标识一律用裸名。"""
    return GIT_SUFFIX.sub("", (raw or "").strip())


def is_valid_name(name: str) -> bool:
    if not name or name in (".", ".."):
        return False
    return "/" not in name and "\\" not in name


def check_source(src: Path) -> None:
    """确认源仓库可用。报错要在打包前给出，而不是等 git 吐内部错误。"""
    if not (src / ".git").exists() and not (src / "HEAD").exists():
        raise Failure(f"不是 git 仓库: {src}")

    # 浅克隆只有部分历史，repack 遍历父提交时会报 Could not read <sha>，
    # 那句话看不出是深度造成的，所以在这里提前拦下。
    if (src / ".git" / "shallow").exists() or (src / "shallow").exists():
        raise Failure(
            "源仓库是浅克隆，历史不完整，打包会失败。\n"
            f'  先补全历史： git -C "{src}" fetch --unshallow\n'
            "  或者重新完整克隆一次。"
        )

    if not try_git(["rev-parse", "--git-dir"], src)[0]:
        raise Failure(f"这个目录不是一个可用的 git 仓库: {src}")


def build_bare(src: Path, target: Path) -> str:
    """把源仓库变成裸仓库，返回用了哪条链路。"""
    # 首选 clone --bare
    if try_git(["clone", "--bare", "--quiet", str(src), str(target)])[0]:
        return "clone"

    # 回退：某些环境（沙箱、受限的 sh.exe）下本地传输建不出管道。
    # 注意不能用 --all，那会把 refs/remotes 之类的也带进来。
    print("> clone --bare 不可用，回退到 bundle 链路")
    shutil.rmtree(target, ignore_errors=True)

    # 临时 bundle 放在输出目录旁边：某些环境（沙箱）不允许写系统 TEMP
    bundle = target.parent / f".{target.name}.snapshot.bundle"
    try:
        run_git(["bundle", "create", str(bundle), "--branches", "--tags"], src)

        run_git(["init", "--bare", "--quiet", str(target)])

        # bundle unbundle 只把 refs 列到 stdout，并不会创建它们。
        # 不显式 update-ref，后面 gc --prune=now 会把所有对象当不可达清掉，
        # 结果是一个"成功"但空荡荡的仓库。
        listing = run_git(["bundle", "unbundle", str(bundle)], target)
        created = 0
        for line in listing.splitlines():
            match = re.match(r"^\s*([0-9a-f]{40})\s+(\S+)\s*$", line)
            if not match:
                continue
            run_git(["update-ref", match.group(2), match.group(1)], target)
            created += 1

        if created == 0:
            raise Failure("bundle 里没有任何 ref")

        head_ref = run_git(["symbolic-ref", "HEAD"], src).strip()
        run_git(["symbolic-ref", "HEAD", head_ref], target)
    finally:
        bundle.unlink(missing_ok=True)

    return "bundle"


def prune(target: Path) -> None:
    """清掉 dumb 协议不会请求的文件。

    清理是尽力而为：某些环境（沙箱、被占用的句柄）会拒绝删除单个文件，
    那不影响仓库能否被 clone，所以不因此中断。
    """

    def remove(path: Path) -> None:
        try:
            if path.is_dir():
                shutil.rmtree(path, ignore_errors=True)
            elif path.exists():
                path.unlink()
        except OSError:
            pass

    for name in JUNK_DIRS:
        remove(target / name)

    for relative in JUNK_FILES:
        remove(target / relative)

    pack_dir = target / "objects" / "pack"
    if pack_dir.is_dir():
        for entry in pack_dir.iterdir():
            if entry.suffix in JUNK_PACK_SUFFIXES:
                remove(entry)


def walk_files(root: Path) -> list[Path]:
    return sorted(p.relative_to(root) for p in root.rglob("*") if p.is_file())


def format_size(size: int) -> str:
    if size < 1024:
        return f"{size} B"
    if size < 1024 * 1024:
        return f"{size / 1024:.1f} KiB"
    return f"{size / 1024 / 1024:.2f} MiB"


def load_registry(path: Path) -> list[dict]:
    if not path.exists():
        return []
    try:
        parsed = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        print("! repository.json 解析失败，将重新生成")
        return []

    items = parsed.get("repositories")
    if not isinstance(items, list):
        return []

    out = []
    for item in items:
        if not isinstance(item, dict) or not item.get("name"):
            continue
        out.append({
            "name": str(item["name"]),
            "description": str(item.get("description") or ""),
        })
    return out


def save_registry(path: Path, entries: list[dict]) -> None:
    """手写 JSON，保证 repositories 始终是数组，缩进也稳定。"""
    body = ",\n".join(
        '    { "name": %s, "description": %s }'
        % (json.dumps(e["name"], ensure_ascii=False),
           json.dumps(e["description"], ensure_ascii=False))
        for e in entries
    )
    path.write_text(f'{{\n  "repositories": [\n{body}\n  ]\n}}\n', encoding="utf-8")


def usage(exit_code: int = 0) -> None:
    print("""用法: python scripts/prepare-repo.py <源仓库> [输出目录] [仓库名]

参数:
  <源仓库>      必填。要转换的仓库路径，普通仓库和裸仓库都行。
  [输出目录]    默认取脚本旁边的 ../public。站点根目录，即放着 index.html 的那个。
  [仓库名]      默认取源目录名。带不带 .git 后缀等价。

示例:
  python scripts/prepare-repo.py ../p2ping                  # 只给源仓库
  python scripts/prepare-repo.py ../p2ping site             # 指定输出目录
  python scripts/prepare-repo.py ../p2ping site my-repo     # 三个都指定

说明:
  产出 <输出目录>/<仓库名>.git/ 和（必要时）repository.json，
  把整个输出目录部署到任意静态托管即可。

  输出目录里如果没有 index.html，说明还缺前端文件，脚本会在结尾提醒。

  重复执行是安全的：同名仓库会被覆盖重建，而 repository.json 里该条目的
  description 会保留下来。""")
    sys.exit(exit_code)


def main(argv: list[str]) -> int:
    if not argv or argv[0] in ("-h", "--help"):
        usage(0 if argv else 1)

    src_arg = argv[0]
    name_arg = argv[2] if len(argv) > 2 else None

    # 默认输出目录取脚本旁边的 ../public，跟从哪个目录调用无关。
    # 否则 cd 到别处跑就会往错误的地方写。
    script_dir = Path(__file__).resolve().parent
    out_arg = argv[1] if len(argv) > 1 else script_dir.parent / "public"

    src = Path(src_arg).expanduser().resolve()
    out_root = Path(out_arg).expanduser().resolve()

    if not src.exists():
        raise Failure(f"源仓库不存在: {src}")

    name = normalize_name(name_arg or src.name)
    if not is_valid_name(name):
        raise Failure(f"非法仓库名: {name!r}")

    check_source(src)

    target = out_root / f"{name}.git"

    print(f"源仓库   {src}")
    print(f"目标     {target}")

    shutil.rmtree(target, ignore_errors=True)
    out_root.mkdir(parents=True, exist_ok=True)

    build_bare(src, target)

    # 对象收进单个 pack，清掉松散对象，再生成服务端索引
    run_git([*NO_AUX_INDEX, "repack", "-a", "-d", "-q"], target)
    run_git([*NO_AUX_INDEX, "gc", "--prune=now", "--quiet"], target)
    run_git([*NO_AUX_INDEX, "update-server-info"], target)

    prune(target)

    missing = [str(rel) for rel in REQUIRED if not (target / rel).exists()]
    if missing:
        print("x 缺少必需文件，生成结果不完整：")
        for rel in missing:
            print(f"   {rel}")
        return 1

    files = walk_files(target)
    total = sum((target / rel).stat().st_size for rel in files)

    print()
    print(f"文件清单 ({len(files)} 个, {format_size(total)})")
    for rel in files:
        size = (target / rel).stat().st_size
        slash = str(rel).replace("\\", "/")
        print(f"   {format_size(size):>10}  {name}.git/{slash}")

    # 维护 repository.json，同名条目保留原有 description
    registry_path = out_root / "repository.json"
    entries = load_registry(registry_path)

    existing = next((e for e in entries if e["name"] == name), None)
    description = existing["description"] if existing else ""

    entries = [e for e in entries if e["name"] != name]
    entries.append({"name": name, "description": description})
    entries.sort(key=lambda e: e["name"])

    save_registry(registry_path, entries)
    print()
    print(f"v 已写入 {registry_path}")

    head = run_git(["symbolic-ref", "--short", "HEAD"], target).strip()
    print(f"v 完成。默认分支 {head}，可直接部署 {out_root}")
    print()
    print(f"  git clone <你的站点>/{name}.git")

    # 脚本只管仓库，不管前端。输出目录得自己带 index.html 才能当站点用。
    if not (out_root / "index.html").exists():
        print()
        print(f"! {out_root} 里没有 index.html，现在还不能直接当站点部署。")
        print("  还差前端文件：把项目里的 index.html 和 src/ 放进同一个目录。")

    print()
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except Failure as exc:
        print(f"x {exc}")
        sys.exit(1)
    except KeyboardInterrupt:
        sys.exit(130)
