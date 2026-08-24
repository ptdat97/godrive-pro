import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "GoDrive · Bảng điều khiển vận hành",
  description: "Quản trị nền tảng gọi xe GoDrive",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="vi">
      <body className="bg-zinc-50 text-zinc-900 antialiased">{children}</body>
    </html>
  );
}
