"use client";

import { useActionState } from "react";
import { useFormStatus } from "react-dom";
import { requestOtp, verifyOtp, type OtpState } from "@/lib/session";

const initialOtp: OtpState = { ok: false };

export default function LoginForm() {
  const [otp, sendOtp] = useActionState(requestOtp, initialOtp);
  const [verify, doVerify] = useActionState(verifyOtp, {});

  // Bước 1: nhập số điện thoại.
  if (!otp.ok) {
    return (
      <form
        action={sendOtp}
        className="rounded-xl border border-zinc-200 bg-white p-6 shadow-sm"
      >
        <label
          htmlFor="phone"
          className="block text-sm font-medium text-zinc-700"
        >
          Số điện thoại
        </label>
        <input
          id="phone"
          name="phone"
          type="tel"
          autoComplete="tel"
          autoFocus
          placeholder="0901234567"
          className="mt-1.5 w-full rounded-lg border border-zinc-300 px-3 py-2 text-sm outline-none focus:border-zinc-900 focus:ring-1 focus:ring-zinc-900"
        />
        {otp.error && <FieldError code={otp.code}>{otp.error}</FieldError>}
        <Submit idle="Gửi mã xác thực" busy="Đang gửi…" />
      </form>
    );
  }

  // Bước 2: nhập mã OTP.
  return (
    <form
      action={doVerify}
      className="rounded-xl border border-zinc-200 bg-white p-6 shadow-sm"
    >
      <input type="hidden" name="challenge_id" value={otp.challengeId} />
      <p className="text-sm text-zinc-600">
        Mã xác thực đã gửi tới{" "}
        <span className="font-medium text-zinc-900">{otp.phone}</span>
      </p>

      {otp.devCode && (
        <p className="mt-3 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800 ring-1 ring-amber-200 ring-inset">
          Chế độ dev — mã:{" "}
          <span className="font-mono font-semibold">{otp.devCode}</span>
        </p>
      )}

      <label
        htmlFor="code"
        className="mt-4 block text-sm font-medium text-zinc-700"
      >
        Mã xác thực
      </label>
      <input
        id="code"
        name="code"
        inputMode="numeric"
        autoComplete="one-time-code"
        autoFocus
        maxLength={6}
        defaultValue={otp.devCode ?? ""}
        placeholder="000000"
        className="mt-1.5 w-full rounded-lg border border-zinc-300 px-3 py-2 text-center font-mono text-lg tracking-[0.4em] outline-none focus:border-zinc-900 focus:ring-1 focus:ring-zinc-900"
      />
      {verify.error && <FieldError code={verify.code}>{verify.error}</FieldError>}
      <Submit idle="Đăng nhập" busy="Đang kiểm tra…" />
    </form>
  );
}

function Submit({ idle, busy }: { idle: string; busy: string }) {
  const { pending } = useFormStatus();
  return (
    <button
      type="submit"
      disabled={pending}
      className="mt-4 w-full rounded-lg bg-zinc-900 px-4 py-2 text-sm font-medium text-white transition hover:bg-zinc-800 disabled:cursor-not-allowed disabled:opacity-50"
    >
      {pending ? busy : idle}
    </button>
  );
}

function FieldError({
  children,
  code,
}: {
  children: React.ReactNode;
  code?: string;
}) {
  return (
    <p className="mt-2 text-sm text-red-600">
      {children}
      {code && <span className="ml-1 font-mono text-xs text-red-400">({code})</span>}
    </p>
  );
}
