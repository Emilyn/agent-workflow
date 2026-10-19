const base = (size) => ({ width: size, height: size, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'aria-hidden': true, strokeLinecap: 'round', strokeLinejoin: 'round' });

export const Spark = ({ size = 18 }) => (
  <svg {...base(size)} strokeWidth="2.2"><path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5L18 18M18 6l-2.5 2.5M8.5 15.5L6 18" /></svg>
);
export const Person = ({ size = 18 }) => (
  <svg {...base(size)} strokeWidth="2.2"><circle cx="12" cy="8" r="4" /><path d="M4 21c0-4 3.5-7 8-7s8 3 8 7" /></svg>
);
export const Clock = ({ size = 18 }) => (
  <svg {...base(size)} strokeWidth="2.2"><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg>
);
export const Play = ({ size = 14 }) => (
  <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M7 4l13 8-13 8z" /></svg>
);
export const Send = ({ size = 18 }) => (
  <svg {...base(size)} strokeWidth="2.4"><path d="M5 12h14M13 6l6 6-6 6" /></svg>
);
export const Git = ({ size = 20 }) => (
  <svg {...base(size)} strokeWidth="2"><circle cx="6" cy="5" r="2.2" /><circle cx="6" cy="19" r="2.2" /><circle cx="18" cy="9" r="2.2" /><path d="M6 7.2v9.6M18 11.2c0 4-6 3-12 6" /></svg>
);
export const Jira = ({ size = 20 }) => (
  <svg {...base(size)} strokeWidth="2"><rect x="4" y="4" width="16" height="16" rx="3" /><path d="M8 12l3 3 5-6" /></svg>
);
export const Figma = ({ size = 20 }) => (
  <svg {...base(size)} strokeWidth="2"><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M3 9h18M9 21V9" /></svg>
);

export const INTEGRATION_ICON = { github: Git, gitlab: Git, jira: Jira, figma: Figma };
