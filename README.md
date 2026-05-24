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
hog            # sample 5s, print apps sorted by CPU
```

(more usage documented as commands land)
