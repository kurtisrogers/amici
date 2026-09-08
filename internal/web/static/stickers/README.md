# Stickers

Decoration members can use on their profile pages, served from Amici's own
origin.

They exist because the canvas sanitiser refuses to render an image hosted
anywhere else. An external image is a request to somebody else's server, and
that request carries the address of every person who looked at the profile,
including people who never agreed to be counted by that host. Rather than
leave members with nothing but gradients, we ship a set.

Rules for anything added here:

- Plain SVG, no external references. No `<image href>`, no `@import`, no
  webfonts, and no `<script>`. The file is served to a browser as-is.
- Small. These are inline decoration, not artwork.
- Named after the thing they show, lowercase, one word. The filename is the
  URL a member types, so `heart.svg` and not `heart-v2-final.svg`.
- Add the name to the `stickers` list in `internal/web/handlers_canvas.go`,
  which is what the editor shows people.
