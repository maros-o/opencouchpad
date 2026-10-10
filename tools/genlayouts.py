#!/usr/bin/env python3
"""Generate internal/keymap/layouts_gen.go from the system XKB database.

Linux uinput can only press physical keys, so typing a character requires
knowing which key (and modifiers) produce it in the layout the target desktop
uses. This script asks libxkbcommon (`xkbcli compile-keymap`) for a fully
resolved keymap per layout, maps keysyms to Unicode via xkbcommon-keysyms.h and
dead-key sequences via the X11 Compose table, then emits compact Go tables.

Requirements (build machine only): xkbcli (libxkbcommon tools), xkeyboard-config,
xkbcommon headers, X11 locale Compose file.

Usage: python3 tools/genlayouts.py > internal/keymap/layouts_gen.go
"""
import re
import subprocess
import sys
import unicodedata

# id -> (xkb layout, xkb variant, human name)
LAYOUTS = [
    ("us", "us", "", "English (US)"),
    ("us(intl)", "us", "intl", "English (US, intl., dead keys)"),
    ("gb", "gb", "", "English (UK)"),
    ("de", "de", "", "German"),
    ("de(nodeadkeys)", "de", "nodeadkeys", "German (no dead keys)"),
    ("at", "at", "", "German (Austria)"),
    ("ch", "ch", "", "German (Switzerland)"),
    ("ch(fr)", "ch", "fr", "French (Switzerland)"),
    ("fr", "fr", "", "French (AZERTY)"),
    ("be", "be", "", "Belgian"),
    ("es", "es", "", "Spanish"),
    ("latam", "latam", "", "Spanish (Latin American)"),
    ("pt", "pt", "", "Portuguese"),
    ("br", "br", "", "Portuguese (Brazil)"),
    ("it", "it", "", "Italian"),
    ("nl", "nl", "", "Dutch"),
    ("se", "se", "", "Swedish"),
    ("no", "no", "", "Norwegian"),
    ("dk", "dk", "", "Danish"),
    ("fi", "fi", "", "Finnish"),
    ("is", "is", "", "Icelandic"),
    ("pl", "pl", "", "Polish"),
    ("cz", "cz", "", "Czech"),
    ("cz(qwerty)", "cz", "qwerty", "Czech (QWERTY)"),
    ("sk", "sk", "", "Slovak"),
    ("sk(qwerty)", "sk", "qwerty", "Slovak (QWERTY)"),
    ("hu", "hu", "", "Hungarian"),
    ("si", "si", "", "Slovenian"),
    ("hr", "hr", "", "Croatian"),
    ("ro", "ro", "", "Romanian"),
    ("tr", "tr", "", "Turkish"),
    ("lt", "lt", "", "Lithuanian"),
    ("lv", "lv", "", "Latvian"),
    ("ee", "ee", "", "Estonian"),
]

# Only keys that produce printable characters.
TYPING_KEYS = (
    ["TLDE"] + ["AE%02d" % i for i in range(1, 13)]
    + ["AD%02d" % i for i in range(1, 13)]
    + ["AC%02d" % i for i in range(1, 13)]
    + ["BKSL", "LSGT"]
    + ["AB%02d" % i for i in range(1, 11)]
)

SHIFT, ALTGR = 1, 2
LEVEL_MODS = [0, SHIFT, ALTGR, SHIFT | ALTGR]


def load_keysyms():
    names = {}
    pat = re.compile(r"#define XKB_KEY_(\w+)\s+0x([0-9a-fA-F]+)(?:\s*/\*\s*U\+([0-9A-Fa-f]+))?")
    with open("/usr/include/xkbcommon/xkbcommon-keysyms.h") as f:
        for line in f:
            m = pat.match(line)
            if not m:
                continue
            name, val, uni = m.group(1), int(m.group(2), 16), m.group(3)
            if uni:
                names[name] = int(uni, 16)
            elif 0x20 <= val <= 0x7E or 0xA0 <= val <= 0xFF:
                names[name] = val
            elif 0x01000000 <= val <= 0x0110FFFF:
                names[name] = val - 0x01000000
            elif name.startswith("dead_"):
                names[name] = ("dead", name)
    return names


KEYSYMS = load_keysyms()


def keysym_value(name):
    """Return int codepoint, ('dead', name) or None."""
    if name in KEYSYMS:
        return KEYSYMS[name]
    m = re.fullmatch(r"U([0-9A-Fa-f]{4,6})", name)
    if m:
        return int(m.group(1), 16)
    m = re.fullmatch(r"0x0*1([0-9A-Fa-f]{6})", name)
    if m:
        return int(m.group(1), 16)
    if name.startswith("dead_"):
        return ("dead", name)
    return None


def load_compose():
    """(dead_keysym_name, keysym_name) -> char, for 2-step dead key sequences."""
    out = {}
    pat = re.compile(r'^<(dead_\w+)>\s+<(\w+)>\s*:\s*"((?:[^"\\]|\\.)+)"')
    with open("/usr/share/X11/locale/en_US.UTF-8/Compose", encoding="utf-8") as f:
        for line in f:
            m = pat.match(line)
            if not m:
                continue
            s = m.group(3).replace('\\"', '"').replace("\\\\", "\\")
            if len(s) == 1:
                out[(m.group(1), m.group(2))] = s
    return out


COMPOSE = load_compose()


def parse_keymap(text):
    keycodes = {}
    for m in re.finditer(r"<(\w+)>\s*=\s*(\d+);", text):
        keycodes.setdefault(m.group(1), int(m.group(2)))
    aliases = dict(re.findall(r"alias <(\w+)>\s*=\s*<(\w+)>;", text))
    sym_start = text.index("xkb_symbols")
    syms = text[sym_start:]
    keys = {}
    for m in re.finditer(r"key <(\w+)>\s*\{(.*?)\};", syms, re.S):
        body = m.group(2)
        sm = re.search(r"symbols\[1\]\s*=\s*\[([^\]]*)\]", body) or re.search(r"\[([^\]]*)\]", body)
        if not sm:
            continue
        levels = [s.strip() for s in sm.group(1).split(",")]
        name = aliases.get(m.group(1), m.group(1))
        keys[name] = levels
    level3 = "ISO_Level3_Shift" in (keys.get("RALT") or [])
    return keycodes, keys, level3


def build(layout, variant):
    cmd = ["xkbcli", "compile-keymap", "--layout", layout]
    if variant:
        cmd += ["--variant", variant]
    text = subprocess.run(cmd, check=True, capture_output=True, text=True).stdout
    keycodes, keys, level3 = parse_keymap(text)

    direct = {}  # char -> (cost, key, mods)
    dead = {}  # dead keysym name -> (cost, key, mods)
    sym_to_stroke = {}  # keysym name -> (cost, key, mods)  (for compose base lookup)
    for kname in TYPING_KEYS:
        if kname not in keys or kname not in keycodes:
            continue
        code = keycodes[kname] - 8
        for lvl, sym in enumerate(keys[kname][:4]):
            mods = LEVEL_MODS[lvl]
            if mods & ALTGR and not level3:
                continue
            cost = (1 if mods & SHIFT else 0) + (2 if mods & ALTGR else 0)
            val = keysym_value(sym)
            if val is None:
                continue
            if sym not in sym_to_stroke or cost < sym_to_stroke[sym][0]:
                sym_to_stroke[sym] = (cost, code, mods)
            if isinstance(val, tuple):
                if sym not in dead or cost < dead[sym][0]:
                    dead[sym] = (cost, code, mods)
                continue
            ch = chr(val)
            if not ch.isprintable() or ch == " ":
                continue
            if ch not in direct or cost < direct[ch][0]:
                direct[ch] = (cost, code, mods)

    # space is always KEY_SPACE; used as the base of "dead key + space".
    sym_to_stroke["space"] = (0, 57, 0)
    composed = {}  # char -> (cost, deadkey, deadmods, key, mods)
    for (dsym, bsym), ch in COMPOSE.items():
        if ch in direct or dsym not in dead or bsym not in sym_to_stroke:
            continue
        if not ch.isprintable():
            continue
        dc, dk, dm = dead[dsym]
        bc, bk, bm = sym_to_stroke[bsym]
        if isinstance(keysym_value(bsym), tuple):
            continue
        cost = 10 + dc + bc
        if ch not in composed or cost < composed[ch][0]:
            composed[ch] = (cost, dk, dm, bk, bm)
    return direct, composed


def go_rune(ch):
    if ch == "'":
        return r"'\''"
    if ch == "\\":
        return r"'\\'"
    if unicodedata.category(ch).startswith(("M", "C", "Z")):
        return "0x%04x" % ord(ch)
    return "'%s'" % ch


def main():
    out = []
    out.append("// Code generated by tools/genlayouts.py from xkeyboard-config; DO NOT EDIT.\n")
    out.append("package keymap\n")
    out.append("var generated = []*Layout{")
    for lid, layout, variant, human in LAYOUTS:
        try:
            direct, composed = build(layout, variant)
        except subprocess.CalledProcessError as e:
            print("skip %s: %s" % (lid, e), file=sys.stderr)
            continue
        out.append("\t{ID: \"%s\", Name: \"%s\", Strokes: map[rune]Stroke{" % (lid, human))
        items = []
        for ch in sorted(direct):
            _, k, m = direct[ch]
            items.append("%s: {%d, %d, 0, 0}" % (go_rune(ch), k, m))
        for ch in sorted(composed):
            _, dk, dm, k, m = composed[ch]
            items.append("%s: {%d, %d, %d, %d}" % (go_rune(ch), k, m, dk, dm))
        for i in range(0, len(items), 6):
            out.append("\t\t" + ", ".join(items[i:i + 6]) + ",")
        out.append("\t}},")
        print("%-16s direct=%d composed=%d" % (lid, len(direct), len(composed)), file=sys.stderr)
    out.append("}")
    print("\n".join(out))


if __name__ == "__main__":
    main()
