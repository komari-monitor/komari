import React from "react";
import type { ErrorInfo, ReactNode } from "react";
import i18n from "@/i18n/config";

interface ErrorBoundaryProps {
  children: ReactNode;
}

interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
}

class ErrorBoundary extends React.Component<
  ErrorBoundaryProps,
  ErrorBoundaryState
> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error): Partial<ErrorBoundaryState> {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Caught error in ErrorBoundary:", error, info);
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="km-error-boundary flex flex-col items-center justify-center min-h-[300px] p-8 bg-gradient-to-br from-rose-50 to-red-50 border border-red-200 rounded-md shadow-md max-w-3xl mx-auto">
          {/* Icon for visual emphasis */}
          <svg
            className="w-12 h-12 text-red-500 mb-4"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            xmlns="http://www.w3.org/2000/svg"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="2"
              d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
            />
          </svg>

          {/* Title */}
          <h2 className="text-3xl font-semibold text-red-700 mb-3 tracking-tight">
            {i18n.t("errorBoundary.something_went_wrong", "Something Went Wrong")}
          </h2>

          {/* Error Message */}
          <p className="text-red-600 text-lg mb-6 font-medium text-center">
            {this.state.error?.message || i18n.t("errorBoundary.unexpected_error", "An unexpected error occurred.")}
          </p>

          {/* Error Details */}
          <details className="w-full max-w-xl">
            <summary className="cursor-pointer text-sm font-medium text-red-500 hover:text-red-700 transition-colors duration-200">
              {i18n.t("errorBoundary.view_error_details", "View Error Details")}
            </summary>
            <pre className="mt-3 p-4 bg-red-100 text-sm text-red-900 border border-red-200 rounded-lg shadow-sm overflow-x-auto">
              {this.state.error?.stack || i18n.t("errorBoundary.no_stack_trace", "No stack trace available.")}
            </pre>
          </details>

          {/* Action Buttons */}
          <div className="mt-6 flex gap-4">
            <button
              onClick={() => window.location.reload()}
              className="px-6 py-2 bg-red-600 text-white rounded-lg shadow hover:bg-red-700 transition-all duration-200 ease-in-out transform hover:scale-105 focus:outline-none focus:ring-2 focus:ring-red-500 focus:ring-opacity-50"
            >
              {i18n.t("errorBoundary.reload_page", "Reload Page")}
            </button>
            <button
              onClick={() => (window.location.href = "/")}
              className="px-6 py-2 bg-gray-200 text-gray-700 rounded-lg shadow hover:bg-gray-300 transition-all duration-200 ease-in-out transform hover:scale-105 focus:outline-none focus:ring-2 focus:ring-gray-500 focus:ring-opacity-50"
            >
              {i18n.t("errorBoundary.go_to_home", "Go to Home")}
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}

export default ErrorBoundary;
