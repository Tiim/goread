// htmx's default indicator CSS is injected as an inline <style> tag, which
// pageCSP's style-src 'self' blocks; the equivalent rules are defined in
// style.css instead (see .htmx-indicator there), so turn off the injection
// attempt to avoid a wasted CSP violation on every page load.
htmx.config.includeIndicatorStyles = false;
