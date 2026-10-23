#!/usr/bin/env python3
"""Validate the dependency-free landing and its deployment placeholders."""

from __future__ import annotations

from html.parser import HTMLParser
from pathlib import Path
import re


ROOT = Path(__file__).resolve().parent.parent
LANDING = ROOT / "web" / "landing"
GITHUB_URL = "https://github.com/pavel-up-lab/bord"


class LandingParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.ids: set[str] = set()
        self.references: set[str] = set()
        self.inline_scripts = 0
        self.inline_styles = 0
        self.markers: set[str] = set()

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = dict(attrs)
        if identifier := values.get("id"):
            if identifier in self.ids:
                raise SystemExit(f"duplicate HTML id: {identifier}")
            self.ids.add(identifier)
        for attribute in ("href", "src"):
            value = values.get(attribute)
            if value and not value.startswith(("#", "data:")):
                self.references.add(value)
        if tag == "script" and "src" not in values:
            self.inline_scripts += 1
        if tag == "style":
            self.inline_styles += 1
        self.markers.update(name for name, _ in attrs if name.startswith("data-"))


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


def main() -> None:
    required = {"index.html", "styles.css", "config.js", "app.js", "favicon.svg"}
    actual = {path.name for path in LANDING.iterdir() if path.is_file()}
    require(actual == required, f"landing files differ: expected={sorted(required)} actual={sorted(actual)}")

    html = (LANDING / "index.html").read_text(encoding="utf-8")
    css = (LANDING / "styles.css").read_text(encoding="utf-8")
    config = (LANDING / "config.js").read_text(encoding="utf-8")
    app = (LANDING / "app.js").read_text(encoding="utf-8")
    parser = LandingParser()
    parser.feed(html)
    parser.close()

    require(parser.inline_scripts == 0 and parser.inline_styles == 0, "CSP requires external scripts and styles")
    require({"top", "features", "install", "controls", "images", "privacy"} <= parser.ids, "required landing sections missing")
    require(
        {"data-download-link", "data-download-name", "data-install-command", "data-run-command", "data-copy-run"}
        <= parser.markers,
        "interactive download markers missing",
    )
    for reference in parser.references:
        if reference == GITHUB_URL:
            continue
        require("://" not in reference, f"external landing dependency is forbidden: {reference}")
        target = LANDING / reference
        require(target.is_file(), f"missing local landing asset: {reference}")

    for key in ("boardURL", "downloadBaseURL"):
        match = re.search(rf"\b{key}:\s*\"([^\"]*)\"", config)
        require(match is not None and match.group(1) == "", f"{key} must remain an empty deployment placeholder")
    html_without_github = html.replace(GITHUB_URL, "")
    require(
        "http://" not in html_without_github + css + app and "https://" not in html_without_github + css + app,
        "landing source contains an unapproved fixed domain",
    )
    require(html.count(GITHUB_URL) == 2, "GitHub links must be present in navigation and hero actions")
    for fragment in (
        "РАЗДЕЛ / THREADS / НАЗВАНИЕ",
        "THREAD / REPLIES / UPDATED / PREVIEW",
        "тема:classic • Tab панели • ? справка",
        "F4</b> Search",
        "[Настройки ▾]",
        "актуальный iTerm2 и WezTerm",
        "brew upgrade --cask iterm2",
        "winget install --id wez.wezterm -e",
        "sudo apt install kitty",
    ):
        require(fragment in html, f"current TUI preview fragment missing: {fragment}")
    require("prefers-reduced-motion" in css, "reduced-motion fallback missing")
    require("@media (max-width:" in css, "responsive layout rules missing")
    require("navigator.clipboard" in app and "document.execCommand" in app, "clipboard primary/fallback paths missing")
    for goos in ("linux", "darwin", "windows"):
        require(f'data-os="{goos}"' in html, f"download tab missing: {goos}")
    for arch in ("amd64", "arm64"):
        require(f'value="{arch}"' in html, f"architecture missing: {arch}")

    print("landing validation passed: local assets, empty domains, responsive controls, six download targets")


if __name__ == "__main__":
    main()
