import { SVGProps } from "react";

type P = SVGProps<SVGSVGElement> & { size?: number };

function Svg({ size = 20, children, ...rest }: P) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}
      strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" {...rest}>
      {children}
    </svg>
  );
}

export const Play = (p: P) => (
  <Svg {...p}><path d="M7 4.5v15a1 1 0 0 0 1.5.86l12.5-7.5a1 1 0 0 0 0-1.72L8.5 3.64A1 1 0 0 0 7 4.5z" fill="currentColor" stroke="none" /></Svg>
);
export const Pause = (p: P) => (
  <Svg {...p}><rect x="6" y="4" width="4" height="16" rx="1" fill="currentColor" stroke="none" /><rect x="14" y="4" width="4" height="16" rx="1" fill="currentColor" stroke="none" /></Svg>
);
export const Search = (p: P) => (<Svg {...p}><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></Svg>);
export const Home = (p: P) => (<Svg {...p}><path d="M3 10.5 12 3l9 7.5V20a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z" /></Svg>);
export const Film = (p: P) => (<Svg {...p}><rect x="3" y="3" width="18" height="18" rx="3" /><path d="M7 3v18M17 3v18M3 8h4M3 16h4M17 8h4M17 16h4M3 12h18" /></Svg>);
export const Tv = (p: P) => (<Svg {...p}><rect x="2" y="6" width="20" height="14" rx="3" /><path d="m8 2 4 4 4-4" /></Svg>);
export const Settings = (p: P) => (
  <Svg {...p}><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" /></Svg>
);
export const Check = (p: P) => (<Svg {...p}><path d="M20 6 9 17l-5-5" /></Svg>);
export const ChevronLeft = (p: P) => (<Svg {...p}><path d="m15 18-6-6 6-6" /></Svg>);
export const ChevronRight = (p: P) => (<Svg {...p}><path d="m9 18 6-6-6-6" /></Svg>);
export const Back = (p: P) => (<Svg {...p}><path d="M19 12H5M12 19l-7-7 7-7" /></Svg>);
export const Volume = (p: P) => (<Svg {...p}><path d="M11 5 6 9H2v6h4l5 4z" /><path d="M15.5 8.5a5 5 0 0 1 0 7M19 5a10 10 0 0 1 0 14" /></Svg>);
export const Mute = (p: P) => (<Svg {...p}><path d="M11 5 6 9H2v6h4l5 4z" /><path d="m22 9-6 6M16 9l6 6" /></Svg>);
export const Fullscreen = (p: P) => (<Svg {...p}><path d="M8 3H5a2 2 0 0 0-2 2v3M21 8V5a2 2 0 0 0-2-2h-3M3 16v3a2 2 0 0 0 2 2h3M16 21h3a2 2 0 0 0 2-2v-3" /></Svg>);
export const Rewind = (p: P) => (<Svg {...p}><path d="M3 12a9 9 0 1 0 3-6.7L3 8" /><path d="M3 3v5h5" /><text x="12" y="15.5" fontSize="7" textAnchor="middle" fill="currentColor" stroke="none" fontWeight="700">10</text></Svg>);
export const Forward = (p: P) => (<Svg {...p}><path d="M21 12a9 9 0 1 1-3-6.7L21 8" /><path d="M21 3v5h-5" /><text x="12" y="15.5" fontSize="7" textAnchor="middle" fill="currentColor" stroke="none" fontWeight="700">10</text></Svg>);
export const Star = (p: P) => (<Svg {...p}><path d="m12 2 3.1 6.3 6.9 1-5 4.9 1.2 6.8L12 17.8 5.8 21l1.2-6.8-5-4.9 6.9-1z" fill="currentColor" stroke="none" /></Svg>);
export const Eye = (p: P) => (<Svg {...p}><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z" /><circle cx="12" cy="12" r="3" /></Svg>);
export const Wand = (p: P) => (<Svg {...p}><path d="m15 4 5 5L9 20H4v-5zM13 6l5 5" /></Svg>);
export const Refresh = (p: P) => (<Svg {...p}><path d="M21 12a9 9 0 0 1-15.5 6.2L3 16M3 12a9 9 0 0 1 15.5-6.2L21 8" /><path d="M21 3v5h-5M3 21v-5h5" /></Svg>);
export const External = (p: P) => (<Svg {...p}><path d="M15 3h6v6M10 14 21 3M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /></Svg>);
export const Copy = (p: P) => (<Svg {...p}><rect x="9" y="9" width="12" height="12" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></Svg>);
export const Plus = (p: P) => (<Svg {...p}><path d="M12 5v14M5 12h14" /></Svg>);
export const Trash = (p: P) => (<Svg {...p}><path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14" /></Svg>);
export const Folder = (p: P) => (<Svg {...p}><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" /></Svg>);
export const X = (p: P) => (<Svg {...p}><path d="M18 6 6 18M6 6l12 12" /></Svg>);
export const Logout = (p: P) => (<Svg {...p}><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" /></Svg>);
export const Subtitles = (p: P) => (<Svg {...p}><rect x="2" y="5" width="20" height="14" rx="3" /><path d="M7 15h4M15 15h2M7 11h2M13 11h4" /></Svg>);
export const Info = (p: P) => (<Svg {...p}><circle cx="12" cy="12" r="10" /><path d="M12 16v-4M12 8h.01" /></Svg>);
export const Grid = (p: P) => (<Svg {...p}><rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" /><rect x="3" y="14" width="7" height="7" rx="1.5" /><rect x="14" y="14" width="7" height="7" rx="1.5" /></Svg>);
