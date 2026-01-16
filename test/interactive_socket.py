import asyncio
from asyncio.queues import QueueShutDown

from wsproto import ConnectionType, WSConnection
from wsproto.events import (
    AcceptConnection,
    CloseConnection,
    Event,
    Ping,
    Pong,
    RejectConnection,
    Request,
    TextMessage,
)


async def connect():
    # TCP level connection
    reader, writer = await asyncio.open_connection("localhost", 42069)

    ws = WSConnection(ConnectionType.CLIENT)
    req = Request(host="localhost:42069", target="/ws")
    data = ws.send(req)

    # write puts the data to the asyncio transport buffer, event loop eventually will process and send it through the wire
    writer.write(data)

    # in case we have way too many write than the io can handle, we make sure it slows down, and process some of them first before proceeding until it falls below limit
    await writer.drain()

    while True:
        data = await reader.read(4096)
        ws.receive_data(data)

        for event in ws.events():
            if isinstance(event, AcceptConnection):
                return ws, reader, writer
            if isinstance(event, RejectConnection):
                raise ConnectionError(f"Handshake rejected: status={event.status_code}")


async def writer_task(
    ws: WSConnection,
    writer: asyncio.StreamWriter,
    outq: asyncio.Queue[Event],
    stdq: asyncio.Queue[None],
):
    try:
        while True:
            event = await outq.get()

            print("Writer: Got an event")

            data = ws.send(event)
            writer.write(data)

            await writer.drain()
            print("Writer: Sent event")

            if isinstance(event, Pong):
                print("Writer: PONG")
                await stdq.put(None)

            if isinstance(event, CloseConnection):
                print("Writer: mission accomplished, shutting down")
                return

    except QueueShutDown:
        print("Writer: mission accomplished, shutting down")
    except Exception as err:
        print(f"Writer: unknown error: {err!r}, shutting down")


async def reader_task(
    ws: WSConnection,
    reader: asyncio.StreamReader,
    outq: asyncio.Queue[Event],
    stdq: asyncio.Queue[None],
):
    text_frag_payload: str = ""
    message_finished: bool = True

    try:
        while True:
            data = await reader.read(4096)
            ws.receive_data(data)

            for event in ws.events():
                if isinstance(event, TextMessage):
                    print(f"Reader: Text message received, frame payload: {event.data}")
                    text_frag_payload += event.data
                    message_finished = False

                    if event.message_finished:
                        print(
                            f"Reader: all frame received, full frame payload: {text_frag_payload}"
                        )
                        text_frag_payload = ""
                        message_finished = True
                        continue

                if isinstance(event, Ping):
                    await outq.put(Pong(payload=event.payload))

                if isinstance(event, Pong):
                    print(
                        f"Reader: Pong received, with payload {event.payload.decode()}"
                    )

                if isinstance(event, CloseConnection):
                    print("Reader: Close received, shutting down")
                    return

            if message_finished:
                await stdq.put(None)
            else:
                continue

    except asyncio.CancelledError:
        print("Reader: mission accomplished, shutting down")
        return
    except Exception as e:
        print(f"Reader: unknown error, shutting down, {e!r}")
        return


async def stdin_task(outq: asyncio.Queue[Event], stdq: asyncio.Queue[None]):
    try:
        while True:
            print("Type command in the following format: <command>:<optional_payload>")
            print("Avaliable command: quit, fq, ping, pong, text, frag")
            line = await asyncio.to_thread(input, ">> ")
            line = line.strip()
            match line.partition(":"):
                case ("quit", _, payload):
                    print(
                        f"Stdin: User is a quitter, closing the connection with payload {payload}..."
                    )
                    await outq.put(CloseConnection(code=1000, reason=payload))
                    return
                case ("ping", _, payload):
                    print("Stdin: fine i will play ping pong with the server...")
                    await outq.put(Ping(payload=payload.encode()))
                case ("pong", _, payload):
                    print("Stdin: PONG")
                    await outq.put(Pong(payload=payload.encode()))
                case ("text", _, payload):
                    print("Stdin: sending meaningful message to server...", payload)
                    await outq.put(TextMessage(data=payload))
                case ("frag", _, payload):
                    print(
                        "Stdin: sending meaningful, but fragmented message to server...",
                        payload,
                    )
                    if len(payload) < 2:
                        print("Stdin: you have to give me more so i can fragment them")

                    for i, ch in enumerate(payload):
                        print(
                            f"Stdin: fragmented frame {i}, with payload {ch}",
                        )
                        await outq.put(
                            TextMessage(
                                data=ch, message_finished=(i == len(payload) - 1)
                            )
                        )
                case ("fq", _, _):
                    print("Stdin: user forced me to quit, will do so you loser")
                    break

                case _:
                    print("Stdin: Invalid command, i expect better from you")

            # Stall until one complete websocket round trip is completed
            # This util aims to test one event at a time, so it is fine
            await stdq.get()

    except asyncio.CancelledError or asyncio.QueueShutDown:
        print("Stdin: mission accomplished, shutting down")
        return
    except Exception as e:
        print(f"Stdin: unknown error, shutting down, {e!r}")
        return


async def main():
    print("setting up...")
    outq = asyncio.Queue[Event]()
    stdq = asyncio.Queue[None]()

    # i hate python scoping
    try:
        ws, reader, writer = await connect()
        print("i think we are in...")
    except ConnectionError:
        print("we are not in...")
        return
    except Exception as err:
        print(f"Unexpected error while connecting: {err!r}")
        return

    wt = asyncio.create_task(writer_task(ws, writer, outq, stdq))
    rt = asyncio.create_task(reader_task(ws, reader, outq, stdq))
    stdint = asyncio.create_task(stdin_task(outq, stdq))
    print("all tasks is up... wating patiently now...")

    _done, pending = await asyncio.wait(
        {rt, stdint}, return_when=asyncio.FIRST_COMPLETED
    )

    for t in pending:
        _ = t.cancel()

    # allow for queued already event to go through
    outq.shutdown()

    # stop all pending stdin chances
    stdq.shutdown(immediate=True)
    await wt

    writer.close()
    await writer.wait_closed()

    print("Shutdown, seeya")


if __name__ == "__main__":
    asyncio.run(main())
