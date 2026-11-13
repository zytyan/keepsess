"""SSH_HOST, SSH_USER, SSH_PASSWORD and optional KEEPSESS_BINARY from env."""
import os
from keepsess import Keepsess


def main():
    with Keepsess() as ks:
        ks.configure(instance_name="Python SSH example")
        conn = ks.connect(
            protocol="ssh", host=os.environ["SSH_HOST"], port=22,
            username=os.environ["SSH_USER"], password=os.environ["SSH_PASSWORD"],
        )
        result = ks.exec(session=conn.session, command="uname -a")
        print(result.output, end="")
        print("exit_code:", result.exit_code, "warning:", result.warning)


if __name__ == "__main__":
    main()
