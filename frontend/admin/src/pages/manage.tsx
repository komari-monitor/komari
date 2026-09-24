import { useNavigate } from "react-router-dom";
import { useEffect } from "react";

const ManagePage = () => {
  const navigate = useNavigate();

  useEffect(() => {
    navigate("/admin/dashboard", { replace: true });
  }, [navigate]);

  return (
    <div className="km-page-manage flex flex-col items-center justify-center h-screen">
      <p>
        This page is provided for compatibility with Isatidia's frontend
        program.
      </p>
      <p>
        If you are looking for the admin panel, please go to{" "}
        <a href="/admin/dashboard">/admin/dashboard</a>.
      </p>
    </div>
  );
};

export default ManagePage;
