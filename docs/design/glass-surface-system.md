# Omnigate Glass / Surface Design System

Status: ADOPTED 2026-06-14. Governs the frosted-glass surfaces of the app chrome.

## Why

The app renders a full-bleed, often animated game background. Foreground chrome
is therefore **translucent frosted glass** so the background reads through —
that's the intended immersive look. The problem this system fixes: every surface
used to hard-code its own `rgba()` + `blur()`, so "glass" drifted across five
hues (`8,8,14` / `15,15,25` / `16,18,26` / `17,19,25` / `30,30,42`) and eight
blur radii (3–18px) with no relationship between them.

## Principles

1. **Elevation drives opacity + blur.** The higher a surface floats, the more
   opaque and the more blurred it is — so it reads clearly and feels "lifted".
2. **One hue.** All glass is based on `15,15,25` (matching the legacy `--panel`);
   the top overlay layer shifts to `20,20,30` for a touch more presence.
3. **Gold is the only accent.** `--accent` / `--gold-*` are reserved for the
   primary action (Launch button) and active state. Everything else is neutral
   glass so gold stays scarce and meaningful.
4. **Small controls are not panels.** A 56×56 button on a deep `0.62` fill reads
   as a black block (especially over the bottom fade). Small/secondary controls
   use a hairline fill (`rgba(255,255,255,0.06)`), not the panel tokens.

## Tokens (`theme.css :root`)

| Token | Value | Blur | Layer |
|---|---|---|---|
| `--glass-1` / `--glass-1-blur` | `rgba(15,15,25,0.45)` | `8px` | **L1 ambient** |
| `--glass-2` / `--glass-2-blur` | `rgba(15,15,25,0.62)` | `12px` | **L2 raised** |
| `--glass-3` / `--glass-3-blur` | `rgba(20,20,30,0.80)` | `16px` | **L3 overlay** |
| `--glass-modal` / `--glass-modal-blur` | `rgba(15,15,25,0.95)` | `16px` | **modal** |

Usage:
```css
.surface {
  background: var(--glass-2);
  backdrop-filter: blur(var(--glass-2-blur));
  -webkit-backdrop-filter: blur(var(--glass-2-blur));
}
```

## Layer → surface mapping

| Layer | Use | Surfaces |
|---|---|---|
| **L1 ambient** | persistent chrome hugging the background | `.sidebar` |
| **L2 raised** | floating panels / cards | AccountChip body, NewsPanel (最新情報), GachaBoard `.panel`/`.card` |
| **L3 overlay** | transient pop-ups (read-now) | AccountChip dropdown `.account-menu`, `.toast` |
| **modal** | dense / dialog content | ConfirmDialog, `.notif-panel`, `.game-config-popover` |

Borders generally rise with elevation: L2 chrome panels use `--line-2`
(AccountChip body, NewsPanel), L3 overlays use `--border-strong`, modal keeps
its gold edge. Content surfaces may differ for definition — GachaBoard cards
use `--border-strong`, and the sidebar (L1) has no outer border.

## Deliberately NOT on the glass tokens

These have their own visual needs and are intentionally excluded:

- **`.grid-card`** — `rgba(15,15,25,0.5)` + `blur(10px) saturate(1.2)`; the
  saturate lifts the card art's colour through the glass. It's content, not chrome.
- **`.grid-card-status-pill`** — `rgba(0,0,0,0.7)`; needs a dark fill for label
  contrast over arbitrary art.
- **`.toggle`** — a segmented control, not a panel.
- **Launch button (`.launch-btn`)** — gold gradient CTA (the one accent surface).
- **Settings gear (`.game-config-btn`)** — hairline `rgba(255,255,255,0.06)`
  secondary button; hover tints gold to hint the accent without claiming it.

## backdrop-filter gotcha (important)

`backdrop-filter` on an element **establishes a backdrop root**. A descendant's
own `backdrop-filter` then only sees content within that ancestor's box — it
cannot blur anything outside it. So a dropdown rendered *inside* a chip that has
`backdrop-filter` will appear to have **no blur** (it has nothing to blur beyond
the tiny chip box); a deep fill can mask this, a light fill exposes it.

Fix: **`Teleport` the pop-up to `<body>`** so it escapes the ancestor's backdrop
root, then position it `fixed` against the trigger's rect. See `AccountChip.vue`
(`openMenu` computes `menuStyle` from the chip's `getBoundingClientRect`).

## Maintenance

- To retune the whole app's glass, edit the four `:root` tokens only.
- New surface → pick a layer by elevation, reference the token, never hard-code
  `rgba()` / `blur()`.
