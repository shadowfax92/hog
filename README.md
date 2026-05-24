# hog

Find the apps hogging your Mac's CPU and memory — grouped by app, color-coded, with a one-shot kill.

## Why

`htop` lists hundreds of bare processes. When Chrome or an Electron app misbehaves it shows up as ~20 anonymous helper rows, so it's hard to see which *app* is actually eating your machine. `hog` samples for a few seconds, groups processes by their owning app bundle, and shows you the heavy hitters — then lets you kill one.

## Install

Requires Go 1.24+ and macOS.

```sh
make install   # builds ./hog and copies it to ~/bin, codesigned
```

## Usage

```sh
hog                 # sample 5s, rank apps by CPU
hog -d 15           # sample 15s for a steadier signal
hog -m              # rank by memory instead of CPU
hog -n 10           # show only the top 10 apps (0 = all)
```

`hog` samples the process table twice over the window and reports each app's CPU%
over that window (summed across its processes, so 100% ≈ one full core) and its
resident memory. Numbers are colored by how big a share of your machine they are:
green is minor, yellow is noticeable, red is hogging.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-d`, `--duration` | `5` | Sampling window in seconds (min 1; 5–30 gives a steadier read) |
| `-m`, `--mem` | off | Sort by memory instead of CPU |
| `-n`, `--limit` | `20` | Show at most N apps (`0` = all) |
