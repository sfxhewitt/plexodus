#!/usr/bin/env python3
"""
Sets up a demo library for app store reviewers (Google Play, Amazon Appstore).

Store reviewers need to sign in and play something. This script gives them a
library of four freely licensed Blender Foundation films (Creative Commons
Attribution, https://studio.blender.org/films/), and nothing else:

  1. downloads the films into a folder (about 2.4 GB, resumes if cut off)
  2. adds them as a "Store demo" library and scans it
  3. creates a reviewer account that can see only that library
  4. creates an invite code for that library (a year, any number of uses)
  5. prints the text to paste into Play Console and the Amazon developer console

Run it on the server, as the user Plexodus runs as or one that can write the folder:

  python3 <(curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/store-demo.py)

Safe to run again: it skips what is done and gives the reviewer a new password.
Settings (environment): PLEXODUS_URL (default http://127.0.0.1:8484),
PLEXODUS_DEMO_DIR (default ~/plexodus-demo), PLEXODUS_ADMIN (admin username).
"""
import getpass
import http.cookiejar
import json
import os
import secrets
import shutil
import sys
import urllib.error
import urllib.request
import zipfile

URL = os.environ.get("PLEXODUS_URL", "http://127.0.0.1:8484").rstrip("/")
DIR = os.path.expanduser(os.environ.get("PLEXODUS_DEMO_DIR", "~/plexodus-demo"))
LIBRARY = "Store demo"
REVIEWER = "storereview"

# Title (year), download URL. All CC BY, (c) Blender Foundation.
FILMS = [
    ("Sintel (2010)", "https://download.blender.org/durian/movies/Sintel.2010.1080p.mkv"),
    ("Tears of Steel (2012)", "https://download.blender.org/demo/movies/ToS/tears_of_steel_720p.mov"),
    ("Elephants Dream (2006)", "https://download.blender.org/ED/elephantsdream-720-h264-st-aac.mov"),
    ("Big Buck Bunny (2008)", "https://download.blender.org/peach/bigbuckbunny_movies/big_buck_bunny_720p_h264.mov.zip"),
]

tty = sys.stdout.isatty()
B, G, Y, R, C, N = ("\033[1m", "\033[32m", "\033[33m", "\033[31m", "\033[36m", "\033[0m") if tty else ("",) * 6


def step(s): print(f"\n{C}{B}== {s} =={N}")
def ok(s): print(f"{G}✔ {s}{N}")
def warn(s): print(f"{Y}! {s}{N}")
def die(s):
    print(f"{R}✘ {s}{N}", file=sys.stderr)
    sys.exit(1)


def ask(q, default=""):
    try:
        with open("/dev/tty") as t:
            print(f"{B}{q}{N}" + (f" [{default}]" if default else "") + ": ", end="", flush=True)
            a = t.readline().strip()
    except OSError:
        a = input(q + ": ").strip()
    return a or default


jar = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))


def api(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(URL + path, data=data, method=method, headers={"Content-Type": "application/json"})
    try:
        with opener.open(req, timeout=120) as r:
            raw = r.read()
    except urllib.error.HTTPError as e:
        try:
            msg = json.loads(e.read()).get("error", "")
        except Exception:
            msg = ""
        raise RuntimeError(f"{method} {path}: {e.code} {msg}".strip())
    return json.loads(raw) if raw.strip() else None


def download(name, url):
    folder = os.path.join(DIR, "Movies", name)
    os.makedirs(folder, exist_ok=True)
    zipped = url.endswith(".zip")
    final = os.path.join(folder, name + os.path.splitext(url[:-4] if zipped else url)[1])
    if os.path.exists(final):
        ok(f"{name}: already here")
        return
    part = os.path.join(folder, os.path.basename(url) + ".part")
    have = os.path.getsize(part) if os.path.exists(part) else 0
    req = urllib.request.Request(url, headers={"Range": f"bytes={have}-"} if have else {})
    with urllib.request.urlopen(req, timeout=60) as r, open(part, "ab" if have and r.status == 206 else "wb") as f:
        total = int(r.headers.get("Content-Length") or 0) + (have if r.status == 206 else 0)
        done = have if r.status == 206 else 0
        while chunk := r.read(1 << 20):
            f.write(chunk)
            done += len(chunk)
            if tty and total:
                print(f"\r  {name}: {done * 100 // total}% of {total >> 20} MB", end="", flush=True)
    if tty:
        print()
    if zipped:
        with zipfile.ZipFile(part) as z:
            member = max(z.infolist(), key=lambda i: i.file_size)
            with z.open(member) as src, open(final, "wb") as dst:
                shutil.copyfileobj(src, dst, 1 << 20)
        os.remove(part)
    else:
        os.rename(part, final)
    ok(f"{name}: downloaded")


def main():
    step("1. Films (Creative Commons, Blender Foundation)")
    print(f"Saving to {DIR}/Movies")
    for name, url in FILMS:
        try:
            download(name, url)
        except Exception as e:
            die(f"Couldn't download {name}: {e}. Run the script again to carry on.")
    with open(os.path.join(DIR, "CREDITS.txt"), "w") as f:
        f.write("Sintel, Tears of Steel, Elephants Dream and Big Buck Bunny\n"
                "(c) Blender Foundation | studio.blender.org/films | CC BY.\n")

    step("2. Sign in to Plexodus")
    try:
        status = api("GET", "/api/status")
    except Exception as e:
        die(f"Plexodus isn't answering at {URL} ({e}). Set PLEXODUS_URL if it runs elsewhere.")
    if status and status.get("setupRequired"):
        die("Plexodus has no admin account yet. Run setup.sh first.")
    user = os.environ.get("PLEXODUS_ADMIN") or ask("Admin username")
    for _ in range(3):
        try:
            api("POST", "/api/login", {"username": user, "password": getpass.getpass("Admin password: ")})
            break
        except RuntimeError as e:
            warn(str(e))
    else:
        die("Couldn't sign in.")
    try:
        api("GET", "/api/users")
    except RuntimeError:
        die("That account isn't the admin. Sign in as the admin.")
    ok("Signed in.")

    step("3. The Store demo library")
    path = os.path.join(DIR, "Movies")
    lib = next((l for l in api("GET", "/api/libraries") or [] if l.get("name") == LIBRARY), None)
    if lib:
        ok(f"'{LIBRARY}' is already there.")
    else:
        lib = api("POST", "/api/libraries", {"name": LIBRARY, "kind": "movies", "paths": [path]})
        ok(f"Added '{LIBRARY}' ({path}).")
    lib_id = lib["id"]
    try:
        api("POST", f"/api/libraries/{lib_id}/scan")
        ok("Scanning it now (posters arrive within a minute when a TMDB key is set).")
    except RuntimeError as e:
        warn(f"Couldn't start a scan ({e}); press Scan on the library later.")

    step("4. Reviewer account")
    password = "-".join(secrets.token_hex(2) for _ in range(3))
    existing = next((u for u in api("GET", "/api/users") or [] if u.get("username") == REVIEWER), None)
    if existing:
        api("PUT", f"/api/users/{existing['id']}/password", {"password": password})
        uid = existing["id"]
        ok(f"'{REVIEWER}' already exists; gave it a new password.")
    else:
        uid = api("POST", "/api/users", {"username": REVIEWER, "password": password, "isAdmin": False})["id"]
        ok(f"Created '{REVIEWER}'.")
    api("PUT", f"/api/users/{uid}/access", {"allLibraries": False, "libraries": [lib_id]})
    ok(f"It sees only '{LIBRARY}'.")

    step("5. Invite code")
    inv = next((i for i in api("GET", "/api/invites") or []
                if i.get("note") == "Store reviewers" and not i.get("usedBy") and i.get("code")), None)
    if inv:
        code = inv["code"]
        ok(f"Using the existing reviewer code {code}.")
    else:
        inv = api("POST", "/api/invites", {"note": "Store reviewers", "allLibraries": False,
                                           "libraries": [lib_id], "days": 365, "maxUses": 0})
        code = inv.get("code", "")
        if inv.get("codeError"):
            die(inv["codeError"])
        ok(f"Created invite code {code} (a year, any number of uses).")

    step("Paste this into Play Console > App access, and Amazon > Testing instructions")
    print(f"""
Plexodus plays videos from the user's own Plexodus media server. We run a demo
server with freely licensed films (Blender Foundation, CC BY) for review.

1. Open the app. On "Connect to your server", type this invite code in the
   box under "Or use an invite code or address" and press Connect:
   {code}
2. The code opens a sign-up page: choose any username and password and press
   "Create account". (If a sign-in page shows instead, use username
   {REVIEWER}, password {password}.)
3. Pick a film, for example Sintel, and press Play.

On Android TV / Fire TV use the D-pad; Back goes back. The demo server stays
online for the review period.
""")


if __name__ == "__main__":
    main()
