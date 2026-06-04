import type { ReactNode } from "react";

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" className="dark" suppressHydrationWarning>
      <body
        style={{
          margin: 0,
          minHeight: "100vh",
          fontFamily: "system-ui, sans-serif",
          backgroundColor: "rgb(17, 17, 17)",
          color: "rgb(255, 255, 255)",
          colorScheme: "dark",
        }}
      >
        {children}
      </body>
    </html>
  );
}
