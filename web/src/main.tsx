import { isTvMode } from "./tv/detect";

// On a TV (the Android app on Fire TV / Android TV, or ?tv=1 in a browser)
// load the 10-foot interface; everywhere else the regular web UI. Each is its
// own chunk with its own styles, so neither pays for the other.
if (isTvMode()) import("./tv/TvApp");
else import("./webApp");
