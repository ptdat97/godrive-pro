import Link from "next/link";
import { redirect } from "next/navigation";
import { currentAdmin, logout } from "@/lib/session";
import NavLink from "@/components/nav-link";

const NAV = [
  { href: "/", label: "Tổng quan" },
  { href: "/drivers", label: "Tài xế" },
  { href: "/trips", label: "Chuyến đi" },
  { href: "/live", label: "Bản đồ" },
  { href: "/settings", label: "Cấu hình" },
];

export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // Chốt chặn phiên: mọi trang trong nhóm này đều yêu cầu token admin hợp lệ.
  // Backend mới là nơi quyết định quyền — ở đây chỉ hỏi lại nó.
  const admin = await currentAdmin();
  if (!admin) redirect("/login");

  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-10 border-b border-zinc-200 bg-white/90 backdrop-blur">
        <div className="mx-auto flex max-w-7xl items-center gap-6 px-6 py-3">
          <Link href="/" className="font-semibold tracking-tight">
            GoDrive
          </Link>
          <nav className="flex items-center gap-1">
            {NAV.map((item) => (
              <NavLink key={item.href} href={item.href}>
                {item.label}
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3">
            <span className="hidden font-mono text-xs text-zinc-400 sm:inline">
              {admin.accountId}
            </span>
            <form action={logout}>
              <button
                type="submit"
                className="rounded-lg border border-zinc-200 px-3 py-1.5 text-xs font-medium text-zinc-600 transition hover:bg-zinc-50"
              >
                Đăng xuất
              </button>
            </form>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-7xl px-6 py-8">{children}</main>
    </div>
  );
}
