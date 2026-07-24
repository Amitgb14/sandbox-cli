/** Small brand marks kept out of the icon library. */

export function BoundaryMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2.5}
      strokeLinecap="round"
      className={className}
      aria-hidden="true"
    >
      <path d="M12 3v18" />
      <path d="M4 8h4M4 16h4" />
      <path d="M16 8h4M16 16h4" />
    </svg>
  );
}

export function GitHubMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" className={className} aria-hidden="true">
      <path d="M12 .5C5.73.5.67 5.57.67 11.84c0 5.01 3.25 9.26 7.76 10.76.57.1.78-.25.78-.55v-2.1c-3.16.69-3.83-1.35-3.83-1.35-.52-1.32-1.27-1.67-1.27-1.67-1.04-.71.08-.7.08-.7 1.15.08 1.76 1.18 1.76 1.18 1.02 1.75 2.68 1.25 3.33.95.1-.74.4-1.25.72-1.54-2.52-.29-5.17-1.26-5.17-5.6 0-1.24.44-2.25 1.17-3.05-.12-.29-.51-1.45.11-3.02 0 0 .96-.31 3.14 1.17a10.9 10.9 0 0 1 5.72 0c2.18-1.48 3.14-1.17 3.14-1.17.62 1.57.23 2.73.11 3.02.73.8 1.17 1.81 1.17 3.05 0 4.35-2.66 5.31-5.19 5.59.41.36.78 1.05.78 2.12v3.14c0 .3.2.66.79.55 4.5-1.5 7.75-5.75 7.75-10.76C23.33 5.57 18.27.5 12 .5z" />
    </svg>
  );
}
