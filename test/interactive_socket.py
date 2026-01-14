import asyncio
import copy

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
        ws: WSConnection, writer: asyncio.StreamWriter, outq: asyncio.Queue[Event | None], inq: asyncio.Queue[None], stdq: asyncio.Queue[None]
):
    while True:
        event = await outq.get()

        if event == None:
            print("Writer: mission accomplished, shutting down")
            return

        print("Writer: Got an event")

        data = ws.send(event)
        writer.write(data)

        await writer.drain()
        print("Writer: Sent event")
        if isinstance(event, Pong):
            print("Writer: PONG")
            await stdq.put(None)
            continue

        await inq.put(None)


async def reader_task(
        ws: WSConnection, reader: asyncio.StreamReader, outq: asyncio.Queue[Event | None], inq: asyncio.Queue[None], stdq: asyncio.Queue[None]
):
    text_frag_payload: list[str] = []
    bin_frag_payload: bytearray = bytearray()

    try:
        while True:
            # need to handle multiple read
            await inq.get()
            while True:
                data = await reader.read(4096)
                payload_bytes = copy.copy(data)
                ws.receive_data(data)
    
                for event in ws.events():
                    if isinstance(event, Ping):
                        print(
                            f"Reader: Ping received, with payload {event.payload.decode()}, responsing a pong..."
                        )
                        await outq.put(Pong(payload=event.payload))
                        print(
                            f"Reader: Pong initiated with the same payload"
                        )
                    if isinstance(event, Pong):
                        print(f"Reader: Pong received, with payload {event.payload.decode()}")
                    if isinstance(event, CloseConnection):
                        print(f"Reader: Close received, shutting down")
                        return

                await stdq.put(None)
                break
    except asyncio.CancelledError:
        print("Reader: mission accomplished, shutting down")
        return
    except Exception as e:
        print(f"Reader: unknown error, shutting down, {e!r}")
        return


async def stdin_task(outq: asyncio.Queue[Event | None], stdq: asyncio.Queue[None]):
    try:
        while True:
            print("Type command in the following format: <command>:<optional_payload>")
            print("Avaliable command: quit, ping, pong")
            line = await asyncio.to_thread(input, ">> ")
            line = line.strip()
            match line.partition(":"):
                case ("quit", _, payload):
                    print(f"Stdin: User is a quitter, closing the connection with payload {payload}...")
                    await outq.put(CloseConnection(code=1000, reason=payload))
                    return
                case ("ping", _, payload):
                    print("Stdin: fine i will play ping pong with the server...")
                    await outq.put(Ping(payload=payload.encode()))
                case ("pong", _, payload):
                    print("Stdin: PONG")
                    await outq.put(Pong(payload=payload.encode()))
                case _:
                    print("Stdin: Invalid command, i expect better from you")

            # Stall until one complete websocket round trip is completed
            # This util aims to test one event at a time, so it is fine
            await stdq.get()

    except asyncio.CancelledError:
        print("Stdin: mission accomplished, shutting down")
        return
    except Exception as e:
        print(f"Stdin: unknown error, shutting down, {e!r}")
        return


async def main():
    print("setting up...")
    outq = asyncio.Queue[Event | None]()
    inq = asyncio.Queue[None]()
    stdq = asyncio.Queue[None]()
    ws, reader, writer = await connect()
    print("i think we are in...")

    wt = asyncio.create_task(writer_task(ws, writer, outq, inq, stdq))
    rt = asyncio.create_task(reader_task(ws, reader, outq, inq, stdq))
    stdint = asyncio.create_task(stdin_task(outq, stdq))
    print("all tasks is up... wating patiently now...")

    _done, pending = await asyncio.wait(
        {rt, stdint}, return_when=asyncio.ALL_COMPLETED
    )

    for t in pending:
        _ = t.cancel()

    await outq.put(None)
    await wt

    writer.close()
    await writer.wait_closed()

    print("Shutdown, seeya")


if __name__ == "__main__":
    asyncio.run(main())
