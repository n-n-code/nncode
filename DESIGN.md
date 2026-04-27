# DESIGN.md

## Product

Name: nⁿcode  
Type: Open-source software  
Primary surface: Terminal / command-line interface  
Visual direction: Old MS-DOS terminal on a CRT monitor, refined for a professional software product.

The product identity should feel technical, minimal, precise, and retro. It should not feel like a novelty arcade game, hacker parody, or generic cyberpunk theme.

## Logo

Canonical logo text:

nⁿcode

Rules:

- The logo must contain only the exact text: nⁿcode
- No slogan.
- No prompt character.
- No cursor.
- No decorative secondary text.
- No icons unless explicitly requested.
- The superscript ⁿ must remain visibly raised and smaller than the first n.
- The wordmark should use an old MS-DOS / bitmap / monospace terminal style.
- The logo should sit on a black or near-black background.
- Preferred rendering: green CRT phosphor text with subtle bloom and scanlines.
- The final impression should be professional, not gimmicky.

## Brand Personality

nⁿcode is:

- Technical
- Minimal
- Open-source
- Terminal-native
- Sharp
- Efficient
- Retro, but not nostalgic clutter
- Serious enough for engineering tools

nⁿcode is not:

- Playful arcade pixel art
- Neon cyberpunk overload
- Corporate SaaS pastel UI
- Skeuomorphic hardware illustration
- Decorative hacker cliché

## Color System

### Core Colors

| Token | Hex | Usage |
|---|---:|---|
| background.black | #020403 | Main background |
| background.raised | #050A06 | Panels, command output blocks |
| phosphor.green | #39FF45 | Primary text, logo, active elements |
| phosphor.green.dim | #1FAE35 | Borders, secondary text, inactive UI |
| phosphor.green.soft | #88FF8F | Highlights, headings, focused states |
| phosphor.shadow | #083D12 | Subtle borders, dividers, status backgrounds |
| text.muted | #6CA875 | Hints, metadata, disabled text |
| warning.amber | #FFB000 | Warnings only |
| error.red | #FF4D4D | Errors only |

### Color Rules

- Default background should be black or near-black.
- Default text should be green phosphor.
- Use one dominant accent: CRT green.
- Amber and red are reserved for status semantics only.
- Do not introduce blue, purple, pink, or rainbow gradients.
- Avoid pure white text unless required for accessibility in a specific non-terminal context.

## Typography

### Primary Typeface Direction

Use a monospace bitmap or terminal-inspired typeface.

Preferred characteristics:

- MS-DOS style
- VGA/BIOS terminal feel
- Pixel-grid construction
- Monospaced proportions
- Clear lowercase characters
- Legible at small sizes

Acceptable font references:

- IBM VGA-style bitmap fonts
- DOS terminal fonts
- PxPlus IBM VGA
- Perfect DOS VGA
- JetBrains Mono as a fallback for non-logo UI
- IBM Plex Mono as a fallback for documentation

### Typography Rules

- Logo uses bitmap/MS-DOS-style rendering.
- UI text uses monospace.
- Avoid rounded modern SaaS fonts.
- Avoid serif fonts.
- Avoid decorative pixel fonts that reduce readability.
- Headings may be bold, but should remain compact and terminal-like.

## Layout

### General Layout Principles

- Prefer dense, functional layouts.
- Use clear rectangular regions.
- Keep surfaces flat.
- Use borders sparingly.
- Avoid large rounded cards unless needed for readability.
- Avoid shadows in normal UI; simulate depth through border contrast instead.

### Spacing Scale

| Token | Value | Usage |
|---|---:|---|
| space.0 | 0 | Tight terminal rows |
| space.1 | 1ch | Inline padding |
| space.2 | 2ch | Panel padding |
| space.3 | 4ch | Section separation |
| space.4 | 8ch | Large layout separation |

For terminal UIs, prefer character-cell spacing over pixel spacing.

## Borders

Preferred border styles:

ASCII border:

+--------+
| Panel  |
+--------+

Unicode border:

┌────────┐
│ Panel  │
└────────┘

Rules:

- Use dim green for normal borders.
- Use bright green for focused borders.
- Do not overuse double-line borders.
- Do not use decorative ornamental borders.
- Borders should help structure information, not decorate it.

## CRT Treatment

CRT styling may be used for brand surfaces, splash screens, documentation hero sections, and logo renders.

Allowed CRT effects:

- Subtle scanlines
- Soft phosphor glow
- Slight bloom around bright green text
- Mild vignette
- Slight horizontal texture

Avoid:

- Heavy distortion
- Excessive blur
- Fake broken screen effects
- Excessive chromatic aberration
- Overpowering scanlines that reduce readability

## Components

### Logo Block

Usage:

- README header
- CLI splash screen
- Website hero
- Project documentation
- Release images

Content:

nⁿcode

Style:

- Black background
- Bright green bitmap text
- Subtle glow
- Centered
- No additional copy inside the logo block

### CLI Header

Example:

nⁿcode
────────────────────────

Rules:

- The logo may appear as plain text in CLI output.
- Do not add decorative ASCII art unless explicitly requested.
- Keep startup output fast and compact.

### Panels

Panels should use:

- Near-black background
- Dim green border
- Green foreground text
- Bright green title or active state

Example:

┌─ Project ──────────────┐
│ status: ready          │
│ branch: main           │
└────────────────────────┘

### Selected State

Selected items should use a subtle background shift, not a harsh inversion:

- Background: #0A1A0D (deep green, slightly lighter than the main background)
- Text: #88FF8F (soft green)
- Bold text allowed

This keeps the selection readable without the eye-strain of full black-on-green inversion.

### Status Bar

Status bars may use:

- Background: #083D12
- Label: #88FF8F
- Value: #39FF45

Example:

MODE normal  BRANCH main  STATUS ready

### Errors

Errors should be direct and technical.

Use:

- Red foreground
- Black background
- No large warning icons unless needed

Example:

error: config file not found

### Warnings

Warnings use amber only.

Example:

warning: fallback parser enabled

## Motion

For terminal or web animations:

Allowed:

- Subtle cursor blink
- Very light phosphor pulse
- Short terminal reveal
- Scanline movement at low opacity

Avoid:

- Constant flicker
- Fast glitch animation
- Distracting typing effects
- Long startup animations

## Accessibility

- Text must remain readable without relying on glow.
- CRT effects must not reduce contrast.
- Critical status must not rely on color alone.
- Provide text labels for warning and error states.
- Avoid rapid blinking.

## Implementation Notes for Lip Gloss

Use these style concepts:

- Logo: bright green, black background, bold, padded
- Screen: black background, dim green border
- Panel: near-black background, dim green border
- PanelActive: bright green border
- Muted: muted green
- SelectedRow: soft green text on deep green background
- Error: red
- Warning: amber

Canonical logo string for Go:

"nⁿcode"

Do not replace the superscript character with n^ncode, nncode, or n-ncode.

## Image Generation Guidance

When generating logo imagery, use this prompt direction:

Professional open-source software logo wordmark containing only the exact text nⁿcode. Old MS-DOS bitmap terminal font, black CRT monitor background, green phosphor glow, subtle scanlines, minimal centered composition, no extra text, no slogan, no icons, no prompt symbol, no cursor. Professional, clean, retro command-line identity.

Negative constraints:

- No extra words
- No tagline
- No watermark
- No prompt symbol
- No cursor
- No keyboard
- No monitor frame unless explicitly requested
- No random symbols
- No cyberpunk city
- No arcade mascot
