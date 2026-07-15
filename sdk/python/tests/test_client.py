from __future__ import annotations

from pathlib import Path
import sys
from unittest import TestCase
from unittest.mock import patch


sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from windforce_execution import WindforceExecutionClient, WindforceTimeoutError  # noqa: E402


class WindforceExecutionClientTest(TestCase):
    def test_ready_uses_service_readiness_endpoint(self) -> None:
        client = WindforceExecutionClient("http://windforce")
        with patch.object(client, "_request", return_value={"ready": True}) as request:
            self.assertTrue(client.ready())
        request.assert_called_once_with("GET", "/readyz")

    def test_create_run_uses_versioned_workspace_endpoint(self) -> None:
        client = WindforceExecutionClient("http://windforce", workspace="team a")
        with patch.object(
            client,
            "_request",
            return_value={"run_id": "run_a", "job_id": "job_a", "state": "QUEUED", "app": "echo", "action": "run"},
        ) as request:
            run = client.create_run(
                app="echo",
                action="run",
                input={"message": "hello"},
                adapter="queue",
                idempotency_key="message-1",
                client_key="external-client-a",
            )

        self.assertEqual(run.run_id, "run_a")
        self.assertEqual(run.job_id, "job_a")
        request.assert_called_once_with(
            "POST",
            "/execution/v1/workspaces/team%20a/runs",
            {
                "app": "echo",
                "action": "run",
                "input": {"message": "hello"},
                "adapter": "queue",
                "idempotency_key": "message-1",
                "client_key": "external-client-a",
            },
        )

    def test_wait_returns_terminal_run(self) -> None:
        client = WindforceExecutionClient("http://windforce", poll_interval_seconds=0.01)
        with patch.object(
            client,
            "_request",
            side_effect=[
                {"run_id": "run_a", "state": "QUEUED", "app": "echo", "action": "run"},
                {"run_id": "run_a", "state": "SUCCEEDED", "app": "echo", "action": "run"},
            ],
        ):
            run = client.wait("run_a", timeout_seconds=1)
        self.assertEqual(run.state, "SUCCEEDED")

    def test_wait_reports_last_state_on_timeout(self) -> None:
        client = WindforceExecutionClient("http://windforce")
        with patch.object(
            client,
            "_request",
            return_value={"run_id": "run_a", "state": "RUNNING", "app": "echo", "action": "run"},
        ):
            with self.assertRaises(WindforceTimeoutError) as raised:
                client.wait("run_a", timeout_seconds=0)
        self.assertEqual(raised.exception.state, "RUNNING")
