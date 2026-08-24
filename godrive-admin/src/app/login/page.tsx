import { redirect } from "next/navigation";
import { currentAdmin } from "@/lib/session";
import LoginForm from "./login-form";

export const metadata = { title: "Đăng nhập · GoDrive" };

export default async function LoginPage() {
  // Đã đăng nhập rồi thì không cần hiện form nữa.
  if (await currentAdmin()) redirect("/");

  return (
    <main className="flex min-h-screen items-center justify-center bg-zinc-50 px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <h1 className="text-2xl font-semibold tracking-tight text-zinc-900">
            GoDrive
          </h1>
          <p className="mt-1 text-sm text-zinc-500">Bảng điều khiển vận hành</p>
        </div>
        <LoginForm />
        <p className="mt-6 text-center text-xs text-zinc-400">
          Chỉ số điện thoại nằm trong <code>ADMIN_PHONES</code> mới đăng nhập
          được.
        </p>
      </div>
    </main>
  );
}
